package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	ocirepov1 "ocm.software/open-component-model/bindings/go/oci/spec/repository/v1/oci"

	"github.com/Masterminds/semver/v3"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	ocmv1alpha1 "ocm.software/open-component-model/bindings/go/kubernetes/controller/api/v1alpha1"
	ociaccess "ocm.software/open-component-model/bindings/go/oci/spec/access"
	ociaccessv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	ocirepospec "ocm.software/open-component-model/bindings/go/oci/spec/repository"
	ocmpath "ocm.software/open-component-model/bindings/go/oci/spec/repository/path"
	"ocm.software/open-component-model/bindings/go/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openmcp-project/extensibility-utils/pkg/discovery/api/v1alpha1"
	apiv1alpha1 "github.com/openmcp-project/extensibility-utils/pkg/discovery/api/v1alpha1"
	"github.com/openmcp-project/extensibility-utils/pkg/objectmanager"
)

var (
	ociRepoScheme = func() *runtime.Scheme {
		s := runtime.NewScheme()
		ocirepospec.MustAddToScheme(s)
		ocirepospec.MustAddLegacyToScheme(s)
		return s
	}()

	ocmScheme = func() *runtime.Scheme {
		s := runtime.NewScheme()
		ociaccess.MustAddToScheme(s)
		v2.MustAddToScheme(s)
		return s
	}()
)

var (
	ErrComponentNotFound = errors.New("component not found")
	ErrResourceNotFound  = errors.New("resource not found")
)

// Resolver manages Discovery objects and resolves OCM resources from them.
type Resolver interface {
	// Apply creates or updates the Discovery objects on the platform cluster and returns true if a requeue is needed.
	Apply(ctx context.Context) (bool, error)
	// Delete deletes the Discovery objects on the platform cluster and returns true if a requeue is needed.
	Delete(ctx context.Context) (bool, error)
	// Credentials returns a reference to the first docker-config secret found on the base components effective OCM config, or nil if none exists.
	Credentials(ctx context.Context) (*corev1.LocalObjectReference, error)
	// Resolve fetches the Discovery for discId, finds the component matching version and componentMatchFunc if not nil, then returns the first resource matching resourceMatchFunc.
	Resolve(ctx context.Context, discID, requestedComponent, requestedResource, version string) (Resource, error)
	// ListComponentVersions returns the available component versions for discId, optionally filtered by matchFunc, sorted oldest first.
	ListComponentVersions(ctx context.Context, discId string, requestedComponent string) ([]string, error)
}

type resolver struct {
	cluster               *clusters.Cluster
	serviceProvider       string
	namespace             string
	baseComponent         ocmv1alpha1.Component
	discoveryConfigs      map[string]apiv1alpha1.ComponentConfig
	basePath              string
	defaultComponentLabel string
	defaultResourceLabel  string
}

// Config holds the configuration for creating a new Resolver.
type Config struct {
	ServiceProvider  string
	Namespace        string
	BaseComponent    string
	DiscoveryConfigs map[string]apiv1alpha1.ComponentConfig
}

// // ServiceDiscoveryConfig defines a single Discovery object to be managed, with optional selectors to filter components and resources.
// type ServiceDiscoveryConfig struct {
// 	ID map[string]apiv1alpha1.DiscoveryConfig
// }

// Component is a parsed OCM component descriptor entry with its name, version, resources, and labels.
type Component struct {
	Name      string
	Version   string
	Resources []v2.Resource
	Labels    []v2.Label
}

// Resource is a resolved OCM resource with its name, type, and a fully-qualified OCI access URL.
type Resource struct {
	Name      string
	Type      string
	AccessURL string
}

func defaultComponentLabel(providerName string) string {
	return fmt.Sprintf("%s.services.open-control-plane.io/%s", providerName, "component")
}

func defaultResourceLabel(providerName string) string {
	return fmt.Sprintf("%s.services.open-control-plane.io/%s", providerName, "resource")
}

func defaultComponentSelector(providerName string, componentType string) *ocmv1alpha1.Selector {
	s := ocmv1alpha1.Selector{
		MatchLabels: map[string]string{
			fmt.Sprintf("%s.services.open-control-plane.io/%s", providerName, "componentType"): componentType,
		},
	}
	return &s
}

// NewResolver creates a Resolver by fetching the base component and computing its OCI base path.
func NewResolver(ctx context.Context, cfg Config, c *clusters.Cluster) (Resolver, error) {
	baseComponent, err := fetchBaseComponent(ctx, c.Cluster().GetClient(), cfg.BaseComponent, cfg.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch base component: %w", err)
	}
	basePath, err := computeBasePath(baseComponent)
	if err != nil {
		return nil, fmt.Errorf("failed to compute base path: %w", err)
	}
	return &resolver{
		cluster:               c,
		serviceProvider:       cfg.ServiceProvider,
		namespace:             cfg.Namespace,
		baseComponent:         baseComponent,
		discoveryConfigs:      cfg.DiscoveryConfigs,
		basePath:              basePath,
		defaultComponentLabel: defaultComponentLabel(cfg.ServiceProvider),
		defaultResourceLabel:  defaultResourceLabel(cfg.ServiceProvider),
	}, nil
}

func fetchBaseComponent(ctx context.Context, reader client.Reader, name string, namespace string) (ocmv1alpha1.Component, error) {
	component := ocmv1alpha1.Component{}
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &component)
	if apierrors.IsNotFound(err) {
		return ocmv1alpha1.Component{}, fmt.Errorf("base component %s/%s was not found", namespace, name)
	}
	if err != nil {
		return ocmv1alpha1.Component{}, err
	}
	return component, nil
}

func (r *resolver) Apply(ctx context.Context) (bool, error) {
	platformCluster := objectmanager.NewCluster(r.cluster, r.namespace, objectmanager.PlatformCluster)
	manageDiscoveries(r.discoveryConfigs, r.namespace, platformCluster, r.serviceProvider, r.baseComponent.Name)
	mgr := objectmanager.NewManager(r.serviceProvider)
	mgr.AddCluster(platformCluster)
	rr, err := mgr.Apply(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to apply discovery objects: %w", err)
	}
	return rr.Requeue, nil
}

func (r *resolver) Delete(ctx context.Context) (bool, error) {
	platformCluster := objectmanager.NewCluster(r.cluster, r.namespace, objectmanager.PlatformCluster)
	manageDiscoveries(r.discoveryConfigs, r.namespace, platformCluster, r.serviceProvider, r.baseComponent.Name)
	mgr := objectmanager.NewManager(r.serviceProvider)
	mgr.AddCluster(platformCluster)
	rr, err := mgr.Delete(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to delete discovery objects: %w", err)
	}
	return rr.Requeue, nil
}

func manageDiscoveries(discoveryConfigs map[string]apiv1alpha1.ComponentConfig, namespace string, cluster objectmanager.Cluster, serviceProvider string, baseComponent string) {
	for discId, discConfig := range discoveryConfigs {
		dcs := defaultComponentSelector(serviceProvider, discId)
		var rs *ocmv1alpha1.Selector
		if discConfig.SelectorConfig != nil {
			dcs = discConfig.SelectorConfig.ComponentSelector
			rs = discConfig.SelectorConfig.ResourceSelector
		}
		discovery := objectmanager.NewObject(
			&ocmv1alpha1.Discovery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      discoveryObjectName(serviceProvider, discId),
					Namespace: namespace,
				},
				Spec: ocmv1alpha1.DiscoverySpec{
					ComponentRef: corev1.LocalObjectReference{Name: baseComponent},
				},
			},
			objectmanager.ObjectConfig{
				ReconcileFunc: func(_ context.Context, o client.Object) error {
					discovery, ok := o.(*ocmv1alpha1.Discovery)
					if !ok {
						return fmt.Errorf("expected *ocmv1alpha1.Discovery, got %T", o)
					}
					discovery.Spec = ocmv1alpha1.DiscoverySpec{
						ComponentRef:      corev1.LocalObjectReference{Name: baseComponent},
						ComponentSelector: dcs,
						ResourceSelector:  rs,
					}
					return nil
				},
				DeletionPolicy: objectmanager.Delete,
				StatusFunc:     discoveryStatus,
			},
		)
		cluster.AddObject(discovery)
	}
}

func discoveryStatus(object client.Object) objectmanager.ManagedObjectStatus {
	discovery, ok := object.(*ocmv1alpha1.Discovery)
	if !ok {
		return objectmanager.ManagedObjectStatus{Phase: objectmanager.StatusPhaseProgressing, Message: "Unexpected object type."}
	}
	if !discovery.GetDeletionTimestamp().IsZero() {
		return objectmanager.ManagedObjectStatus{Phase: objectmanager.StatusPhaseTerminating, Message: "Resource is terminating."}
	}
	if discovery.GetUID() == "" {
		return objectmanager.ManagedObjectStatus{Phase: objectmanager.StatusPhaseProgressing, Message: "Resource has not been created yet."}
	}
	if discovery.GetGeneration() != discovery.Status.ObservedGeneration {
		return objectmanager.ManagedObjectStatus{Phase: objectmanager.StatusPhaseProgressing, Message: "Resource has not been reconciled yet."}
	}
	if !meta.IsStatusConditionTrue(discovery.GetConditions(), "Ready") {
		return objectmanager.ManagedObjectStatus{Phase: objectmanager.StatusPhaseProgressing, Message: "Resource is not ready."}
	}
	return objectmanager.ManagedObjectStatus{Phase: objectmanager.StatusPhaseReady, Message: "Resource exists."}
}

func (r *resolver) Credentials(ctx context.Context) (*corev1.LocalObjectReference, error) {
	reader := r.cluster.Cluster().GetClient()
	configs := r.baseComponent.GetEffectiveOCMConfig()
	var fetchErrs []error
	for _, config := range configs {
		if config.Kind != "Secret" {
			continue
		}
		ns := config.Namespace
		if ns == "" {
			ns = r.namespace
		}
		secret := corev1.Secret{}
		err := reader.Get(ctx, client.ObjectKey{Name: config.Name, Namespace: ns}, &secret)
		if err != nil {
			fetchErrs = append(fetchErrs, fmt.Errorf("failed to fetch secret %s/%s: %w", ns, config.Name, err))
			continue
		}
		if secret.Type != corev1.SecretTypeDockerConfigJson {
			continue
		}
		return &corev1.LocalObjectReference{Name: config.Name}, nil
	}
	if len(fetchErrs) > 0 {
		return nil, errors.Join(fetchErrs...)
	}
	return nil, nil
}

// ComponentMatchFunc returns true if the given Component should be selected.
type ComponentMatchFunc func(Component) bool

func ComponentByName(name string) ComponentMatchFunc {
	return func(c Component) bool { return c.Name == name }
}
func ComponentByShortName(name string) ComponentMatchFunc {
	return func(c Component) bool { return path.Base(c.Name) == name }
}
func ComponentByLabel(key string, value json.RawMessage) ComponentMatchFunc {
	return func(c Component) bool {
		for _, l := range c.Labels {
			if l.Name == key && bytes.Equal(l.Value, value) {
				return true
			}
		}
		return false
	}
}

func (r *resolver) ListComponentVersions(ctx context.Context, discID string, component string) ([]string, error) {
	discConfig, ok := r.discoveryConfigs[discID]
	if !ok {
		return nil, fmt.Errorf("failed to find discovery id %s", discID)
	}
	discovery, err := r.fetchDiscovery(ctx, discID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch discovery %s: %w", discID, err)
	}
	components, err := parseDiscovery(discovery)
	if err != nil {
		return nil, fmt.Errorf("failed to parse discovery %s: %w", discID, err)
	}
	raw, err := json.Marshal(component)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal component: %w", err)
	}
	cmf := ComponentByLabel(defaultComponentLabel(r.serviceProvider), json.RawMessage(raw))
	if discConfig.ComponentMatchLabelKey != "" {
		cmf = ComponentByLabel(discConfig.ComponentMatchLabelKey, json.RawMessage(raw))
	}

	var availableVersions []string
	for _, component := range components {
		if cmf(component) {
			availableVersions = append(availableVersions, component.Version)
		}
	}
	slices.SortFunc(availableVersions, CompareVersions)
	return availableVersions, nil
}

func determineResourceMatchFunc(cfg *apiv1alpha1.ResourceConfig, requestedResource string, providerName string) (ResourceMatchFunc, error) {
	var rmf ResourceMatchFunc
	var raw []byte
	var err error

	switch {
	case cfg == nil:
		raw, err = json.Marshal(requestedResource)
		rmf = ResourceByLabel(defaultResourceLabel(providerName), json.RawMessage(raw))
	case cfg.Name != "":
		rmf = ResourceByName(requestedResource)
	case cfg.ResourceType != "":
		rmf = ResourceByType(requestedResource)
	case cfg.Label != nil:
		if cfg.Label.Key == "" || cfg.Label.Value == "" {
			err = fmt.Errorf("LabelConfig key and value must be set")
		}
		raw, err = json.Marshal(cfg.Label.Value)
		rmf = ResourceByLabel(cfg.Label.Key, json.RawMessage(raw))
	default:
		err = fmt.Errorf("resource match func cloud not be dertermined")
	}
	return rmf, err
}

// CompareVersions orders two component versions oldest first.
// Versions are compared semantically and versions that do not parse as semver are lexicographically sorted after valid ones.
func CompareVersions(a, b string) int {
	va, errA := semver.NewVersion(a)
	vb, errB := semver.NewVersion(b)
	switch {
	case errA == nil && errB == nil:
		return va.Compare(vb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// ResourceMatchFunc returns true if the given OCM resource should be selected.
type ResourceMatchFunc func(v2.Resource) bool

func ResourceByName(name string) ResourceMatchFunc {
	return func(r v2.Resource) bool { return r.Name == name }
}
func ResourceByShortName(name string) ResourceMatchFunc {
	return func(r v2.Resource) bool { return path.Base(r.Name) == name }
}
func ResourceByType(resourceType string) ResourceMatchFunc {
	return func(r v2.Resource) bool { return r.Type == resourceType }
}
func ResourceByLabel(key string, value json.RawMessage) ResourceMatchFunc {
	return func(r v2.Resource) bool {
		for _, l := range r.Labels {
			if l.Name == key && bytes.Equal(l.Value, value) {
				return true
			}
		}
		return false
	}
}

func (r *resolver) Resolve(ctx context.Context, discID, requestedComponent, requestedResource, version string) (Resource, error) {
	discConfig, ok := r.discoveryConfigs[discID]
	if !ok {
		return Resource{}, fmt.Errorf("failed to find discovery id %s", discID)
	}
	discovery, err := r.fetchDiscovery(ctx, discID)
	if err != nil {
		return Resource{}, fmt.Errorf("failed to fetch discovery %q: %w", discID, err)
	}
	components, err := parseDiscovery(discovery)
	if err != nil {
		return Resource{}, fmt.Errorf("failed to parse discovery %q: %w", discID, err)
	}
	raw, err := json.Marshal(requestedComponent)
	if err != nil {
		return Resource{}, fmt.Errorf("failed to marshal component: %w", err)
	}
	cmf := ComponentByLabel(defaultComponentLabel(r.serviceProvider), json.RawMessage(raw))
	if discConfig.ComponentMatchLabelKey != "" {
		cmf = ComponentByLabel(discConfig.ComponentMatchLabelKey, json.RawMessage(raw))
	}
	discoveredComponent, ok := findComponent(components, cmf, version)
	if !ok {
		return Resource{}, fmt.Errorf("failed to find component for version %q: %w", version, ErrComponentNotFound)
	}
	var rcPtr *v1alpha1.ResourceConfig
	if rc, ok := discConfig.ResourceConfig[requestedResource]; ok {
		rcPtr = &rc
	}
	resourceMatchFunc, err := determineResourceMatchFunc(rcPtr, requestedResource, r.serviceProvider)
	if err != nil {
		return Resource{}, fmt.Errorf("failed to determine resource match func: %w", err)
	}
	resource, err := findResource(discoveredComponent, r.basePath, resourceMatchFunc)
	if err != nil {
		return Resource{}, fmt.Errorf("failed to find resource in discovery %q: %w", discID, err)
	}
	return resource, nil
}

func computeBasePath(baseComponent ocmv1alpha1.Component) (string, error) {
	ociRepo, err := ociRepoFromComponent(&baseComponent)
	if err != nil {
		return "", err
	}

	basePath, err := componentDescriptorBase(ociRepo)
	if err != nil {
		return "", err
	}
	return basePath, nil
}

func (r *resolver) fetchDiscovery(ctx context.Context, id string) (ocmv1alpha1.Discovery, error) {
	discovery := ocmv1alpha1.Discovery{}
	reader := r.cluster.Cluster().GetClient()
	err := reader.Get(ctx, client.ObjectKey{Namespace: r.namespace, Name: discoveryObjectName(r.serviceProvider, id)}, &discovery)
	if apierrors.IsNotFound(err) {
		return ocmv1alpha1.Discovery{}, fmt.Errorf("discovery %q was not found", id)
	}
	if err != nil {
		return ocmv1alpha1.Discovery{}, err
	}
	if discovery.Generation != discovery.Status.ObservedGeneration {
		return ocmv1alpha1.Discovery{}, fmt.Errorf("discovery %q has not been observed for its current spec", id)
	}
	if !meta.IsStatusConditionTrue(discovery.GetConditions(), "Ready") {
		return ocmv1alpha1.Discovery{}, fmt.Errorf("discovery %q is not ready", id)
	}
	return discovery, nil
}

func discoveryObjectName(providerName string, discoveryID string) string {
	return fmt.Sprintf("sp-%s-%s", providerName, discoveryID)
}

func parseDiscovery(discovery ocmv1alpha1.Discovery) ([]Component, error) {
	components := make([]Component, 0, len(discovery.Status.Components))
	for _, raw := range discovery.Status.Components {
		var desc v2.Descriptor
		err := json.Unmarshal(raw.Raw, &desc)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal components of discovery %s: %w", discovery.Name, err)
		}
		component := Component{
			Name:      desc.Component.Name,
			Version:   desc.Component.Version,
			Resources: desc.Component.Resources,
			Labels:    desc.Component.Labels,
		}
		components = append(components, component)
	}
	return components, nil
}

func findComponent(components []Component, componentMatchFunc ComponentMatchFunc, version string) (Component, bool) {
	for _, component := range components {
		if (componentMatchFunc == nil || componentMatchFunc(component)) && normalizeVersion(component.Version) == normalizeVersion(version) {
			return component, true
		}
	}
	return Component{}, false
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(version, "v")
}

func findResource(component Component, basePath string, resourceMatchFunc ResourceMatchFunc) (Resource, error) {
	if resourceMatchFunc == nil {
		return Resource{}, fmt.Errorf("matchFunc must not be nil")
	}
	for _, resource := range component.Resources {
		if resourceMatchFunc(resource) {
			accessURL, err := decodeAccess(resource, basePath, component.Name)
			if err != nil {
				return Resource{}, fmt.Errorf("failed to decode access for resource %q: %w", resource.Name, err)
			}
			return Resource{
				Name:      resource.Name,
				Type:      resource.Type,
				AccessURL: accessURL,
			}, nil
		}
	}
	return Resource{}, fmt.Errorf("no matching resource was found: %w", ErrResourceNotFound)
}

func decodeAccess(resource v2.Resource, basePath string, componentName string) (string, error) {
	if resource.Access == nil {
		return "", fmt.Errorf("resource has no access")
	}

	obj, err := ocmScheme.NewObject(resource.Access.GetType())
	if err != nil {
		return "", fmt.Errorf("unhandled access type %q: %w", resource.Access.GetType(), err)
	}
	if err := ocmScheme.Convert(resource.Access, obj); err != nil {
		return "", fmt.Errorf("failed to convert access: %w", err)
	}

	switch a := obj.(type) {
	case *v2.LocalBlob:
		if a.GlobalAccess != nil {
			img, err := as[*ociaccessv1.OCIImage](ocmScheme, a.GlobalAccess)
			if err != nil {
				return "", err
			}
			return img.ImageReference, nil
		}
		return fmt.Sprintf("%s/%s@%s", basePath, componentName, a.LocalReference), nil

	case *ociaccessv1.OCIImage:
		return a.ImageReference, nil

	default:
		return "", fmt.Errorf("unhandled access type: %s", resource.Access.GetType())
	}
}

func ociRepoFromComponent(component *ocmv1alpha1.Component) (*ocirepov1.Repository, error) {
	componentInfo := component.Status.Component
	if componentInfo.RepositorySpec == nil {
		return nil, fmt.Errorf("component %q has no resolved repositorySpec", component.Name)
	}

	raw := &runtime.Raw{}
	if err := json.Unmarshal(componentInfo.RepositorySpec.Raw, raw); err != nil {
		return nil, fmt.Errorf("failed to decode repositorySpec: %w", err)
	}

	ociRepo, err := as[*ocirepov1.Repository](ociRepoScheme, raw)
	if err != nil {
		return nil, err
	}

	return ociRepo, nil
}

func componentDescriptorBase(repo *ocirepov1.Repository) (string, error) {
	baseURL := strings.TrimSuffix(repo.BaseUrl, "/")
	purl, err := runtime.ParseURLAndAllowNoScheme(baseURL)
	if err != nil {
		return "", fmt.Errorf("could not parse OCI repository URL %q: %w", repo.BaseUrl, err)
	}

	subPath := repo.SubPath
	if subPath == "" && purl.Path != "" && purl.Path != "/" {
		subPath = strings.TrimPrefix(purl.Path, "/")
		baseURL = purl.Host
	}
	if purl.Scheme != "" {
		baseURL = strings.TrimPrefix(baseURL, purl.Scheme+"://")
	}

	basePath := baseURL
	if subPath != "" {
		basePath += "/" + subPath
	}
	return basePath + "/" + ocmpath.DefaultComponentDescriptorPath, nil
}

func as[T runtime.Typed](scheme *runtime.Scheme, raw *runtime.Raw) (T, error) {
	var zero T
	obj, err := scheme.NewObject(raw.GetType())
	if err != nil {
		return zero, fmt.Errorf("unknown type %q: %w", raw.GetType(), err)
	}
	if err := scheme.Convert(raw, obj); err != nil {
		return zero, err
	}
	typed, ok := obj.(T)
	if !ok {
		return zero, fmt.Errorf("expected %T but got type %q", zero, raw.GetType())
	}
	return typed, nil
}
