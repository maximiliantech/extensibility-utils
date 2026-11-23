package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/openmcp-project/extensibility-utils/pkg/discovery"
	apiv1alpha1 "github.com/openmcp-project/extensibility-utils/pkg/discovery/api/v1alpha1"
)

type Resolver struct {
	applyRequeue     bool
	deleteRequeue    bool
	secret           *corev1.LocalObjectReference
	discoveries      map[string][]Component
	serviceProvider  string
	discoveryConfigs map[string]apiv1alpha1.DiscoveryConfig

	// Errors to return from the respective interface methods.
	applyErr        error
	deleteErr       error
	credentialsErr  error
	resolveErr      error
	listVersionsErr error

	// Override the respective fake behavior entirely when set.
	ApplyFunc                 func(ctx context.Context) (bool, error)
	DeleteFunc                func(ctx context.Context) (bool, error)
	CredentialsFunc           func(ctx context.Context) (*corev1.LocalObjectReference, error)
	ResolveFunc               func(ctx context.Context, discID, requestedResource, version string, opts ...discovery.ResolveOption) (string, error)
	ListComponentVersionsFunc func(ctx context.Context, discID string, opts ...discovery.ResolveOption) ([]string, error)
}

type Component struct {
	Name      string
	Version   string
	Labels    map[string]json.RawMessage
	Resources []Resource
}

type Resource struct {
	Name      string
	Type      string
	AccessURL string
	Labels    map[string]json.RawMessage
}

var _ discovery.Resolver = &Resolver{}

func NewResolver() *Resolver {
	return &Resolver{}
}

// WithCrendetials sets the applies requeue value.
func (f *Resolver) WithApplyRequeue(requeue bool) *Resolver {
	f.applyRequeue = requeue
	return f
}

// WithCrendetials sets the deletes requeue value.
func (f *Resolver) WithDeleteRequeue(requeue bool) *Resolver {
	f.deleteRequeue = requeue
	return f
}

// WithCrendetials sets the reference to the secret carrying the credentials.
func (f *Resolver) WithCredentials(secret *corev1.LocalObjectReference) *Resolver {
	f.secret = secret
	return f
}

// WithComponents registers the components under the given discovery ID.
func (f *Resolver) WithComponents(discID string, components []Component) *Resolver {
	if f.discoveries == nil {
		f.discoveries = map[string][]Component{}
	}
	f.discoveries[discID] = components
	return f
}

// WithServiceProvider sets the service provider name used to derive the default component label key.
func (f *Resolver) WithServiceProvider(name string) *Resolver {
	f.serviceProvider = name
	return f
}

// WithDiscoveryConfig registers the DiscoveryConfig for a discovery ID.
func (f *Resolver) WithDiscoveryConfig(discID string, cfg apiv1alpha1.DiscoveryConfig) *Resolver {
	if f.discoveryConfigs == nil {
		f.discoveryConfigs = map[string]apiv1alpha1.DiscoveryConfig{}
	}
	f.discoveryConfigs[discID] = cfg
	return f
}

// WithApplyError sets the error for Apply.
func (f *Resolver) WithApplyError(err error) *Resolver {
	f.applyErr = err
	return f
}

// WithApplyError sets the error for Delete.
func (f *Resolver) WithDeleteError(err error) *Resolver {
	f.deleteErr = err
	return f
}

// WithApplyError sets the error for Credentials.
func (f *Resolver) WithCredentialsError(err error) *Resolver {
	f.credentialsErr = err
	return f
}

// WithApplyError sets the error for Resolve.
func (f *Resolver) WithResolveError(err error) *Resolver {
	f.resolveErr = err
	return f
}

// WithApplyError sets the error for ListComponentVersions.
func (f *Resolver) WithListComponentsVersionsError(err error) *Resolver {
	f.listVersionsErr = err
	return f
}

func (f *Resolver) Apply(ctx context.Context) (bool, error) {
	if f.applyErr != nil {
		return false, f.applyErr
	}
	if f.ApplyFunc != nil {
		return f.ApplyFunc(ctx)
	}
	return f.applyRequeue, nil
}

func (f *Resolver) Delete(ctx context.Context) (bool, error) {
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx)
	}
	return f.deleteRequeue, nil
}

func (f *Resolver) Credentials(ctx context.Context) (*corev1.LocalObjectReference, error) {
	if f.credentialsErr != nil {
		return nil, f.credentialsErr
	}
	if f.CredentialsFunc != nil {
		return f.CredentialsFunc(ctx)
	}
	return f.secret, nil
}

func (f *Resolver) Resolve(ctx context.Context, discID, requestedResource, version string, opts ...discovery.ResolveOption) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	if f.ResolveFunc != nil {
		return f.ResolveFunc(ctx, discID, requestedResource, version, opts...)
	}
	_, ok := f.discoveryConfigs[discID]
	if !ok {
		return "", fmt.Errorf("failed to find discovery id %q", discID)
	}
	requestedComponent := discovery.ApplyResolveOptions(opts...).ComponentLabel
	for _, c := range f.discoveries[discID] {
		if strings.TrimPrefix(c.Version, "v") != strings.TrimPrefix(version, "v") {
			continue
		}
		if !f.componentMatches(discID, c, requestedComponent) {
			continue
		}
		matches, err := f.resourceMatcher(discID, requestedResource)
		if err != nil {
			return "", err
		}
		for _, r := range c.Resources {
			if matches(r) {
				return r.AccessURL, nil
			}
		}
		return "", fmt.Errorf("no resource %q found in component %q: %w", requestedResource, c.Name, discovery.ErrResourceNotFound)
	}
	return "", fmt.Errorf("no component found for version %q in discovery %q: %w", version, discID, discovery.ErrComponentNotFound)
}

func (f *Resolver) ListComponentVersions(ctx context.Context, discID string, opts ...discovery.ResolveOption) ([]string, error) {
	if f.listVersionsErr != nil {
		return nil, f.listVersionsErr
	}
	if f.ListComponentVersionsFunc != nil {
		return f.ListComponentVersionsFunc(ctx, discID, opts...)
	}
	_, ok := f.discoveryConfigs[discID]
	if !ok {
		return nil, fmt.Errorf("failed to find discovery id %q", discID)
	}
	requestedComponent := discovery.ApplyResolveOptions(opts...).ComponentLabel
	var versions []string
	for _, c := range f.discoveries[discID] {
		if f.componentMatches(discID, c, requestedComponent) {
			versions = append(versions, c.Version)
		}
	}
	slices.SortFunc(versions, discovery.CompareVersions)
	return versions, nil
}

func (f *Resolver) componentMatches(discID string, c Component, componentLabel string) bool {
	if componentLabel == "" {
		return true
	}
	key := discovery.DefaultComponentLabelKey(f.serviceProvider)
	cfg, ok := f.discoveryConfigs[discID]
	if ok && cfg.ComponentMatchLabelKey != "" {
		key = cfg.ComponentMatchLabelKey
	}
	return labelHasValue(c.Labels, key, componentLabel)
}

func (f *Resolver) resourceMatcher(discID, requested string) (func(Resource) bool, error) {
	cfg, ok := f.resourceConfig(discID, requested)
	switch {
	case !ok:
		return func(r Resource) bool { return r.Type == requested }, nil
	case cfg.Name != "":
		return func(r Resource) bool { return r.Name == cfg.Name }, nil
	case cfg.ResourceType != "":
		return func(r Resource) bool { return r.Type == cfg.ResourceType }, nil
	case cfg.Label != nil:
		if cfg.Label.Key == "" || cfg.Label.Value == "" {
			return nil, fmt.Errorf("LabelConfig key and value must be set")
		}
		return func(r Resource) bool { return labelHasValue(r.Labels, cfg.Label.Key, cfg.Label.Value) }, nil
	default:
		return nil, fmt.Errorf("no resource match func could be determined")
	}
}

func (f *Resolver) resourceConfig(discID, requested string) (apiv1alpha1.ResourceConfig, bool) {
	dc, ok := f.discoveryConfigs[discID]
	if !ok {
		return apiv1alpha1.ResourceConfig{}, false
	}
	rc, ok := dc.ResourceConfig[requested]
	return rc, ok
}

func labelHasValue(labels map[string]json.RawMessage, key, value string) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	raw, ok := labels[key]
	if !ok {
		return false
	}
	return bytes.Equal(raw, encoded)
}
