package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/openmcp-project/extensibility-utils/pkg/discovery"
)

type Resolver struct {
	requeue    bool
	secret     *corev1.LocalObjectReference
	components []Component

	// Sets errors to return for interface functions
	applyErr        error
	deleteErr       error
	credentialsErr  error
	resolveErr      error
	listVersionsErr error

	// Override respective fake resolver behavior if custom behavior is required
	ApplyFunc        func(ctx context.Context) (bool, error)
	DeleteFunc       func(ctx context.Context) (bool, error)
	CredentialsFunc  func(ctx context.Context) (*corev1.LocalObjectReference, error)
	ResolveFunc      func(ctx context.Context, discID, requestedComponent, requestedResource, version string) (discovery.Resource, error)
	ListVersionsFunc func(ctx context.Context, discId string, requestedComponent string) ([]string, error)
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

func (f *Resolver) WithRequeue(requeue bool) *Resolver {
	f.requeue = requeue
	return f
}

func (f *Resolver) WithCredentials(secret *corev1.LocalObjectReference) *Resolver {
	f.secret = secret
	return f
}

func (f *Resolver) WithComponents(components []Component) *Resolver {
	f.components = components
	return f
}

func (f *Resolver) WithApplyErr(err error) *Resolver {
	f.applyErr = err
	return f
}

func (f *Resolver) WithCredentialsError(err error) *Resolver {
	f.credentialsErr = err
	return f
}

func (f *Resolver) WithResolveError(err error) *Resolver {
	f.resolveErr = err
	return f
}

func (f *Resolver) WithListVersionsError(err error) *Resolver {
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
	return f.requeue, nil
}

func (f *Resolver) Delete(ctx context.Context) (bool, error) {
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx)
	}
	return f.requeue, nil
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

func (f *Resolver) Resolve(ctx context.Context, discID, requestedComponent, requestedResource, version string) (discovery.Resource, error) {
	if f.resolveErr != nil {
		return discovery.Resource{}, f.resolveErr
	}
	if f.ResolveFunc != nil {
		return f.ResolveFunc(ctx, discID, requestedComponent, requestedResource, version)
	}
	for _, c := range f.components {
		if strings.TrimPrefix(c.Version, "v") != strings.TrimPrefix(version, "v") {
			continue
		}
		if requestedComponent != "" && path.Base(c.Name) != requestedComponent && c.Name != requestedComponent {
			continue
		}
		for _, r := range c.Resources {
			if r.Name == requestedResource {
				return discovery.Resource{Name: r.Name, Type: r.Type, AccessURL: r.AccessURL}, nil
			}
		}
	}
	return discovery.Resource{}, fmt.Errorf("no resource found for version %q", version)
}

func (f *Resolver) ListComponentVersions(ctx context.Context, discId string, requestedComponent string) ([]string, error) {
	if f.listVersionsErr != nil {
		return nil, f.listVersionsErr
	}
	if f.ListVersionsFunc != nil {
		return f.ListVersionsFunc(ctx, discId, requestedComponent)
	}
	var versions []string
	for _, c := range f.components {
		if requestedComponent == "" || path.Base(c.Name) == requestedComponent || c.Name == requestedComponent {
			versions = append(versions, c.Version)
		}
	}
	slices.SortFunc(versions, discovery.CompareVersions)
	return versions, nil
}
