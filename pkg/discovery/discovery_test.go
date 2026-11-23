package discovery

import (
	"encoding/json"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	ocmv1alpha1 "ocm.software/open-component-model/bindings/go/kubernetes/controller/api/v1alpha1"
	ocmpath "ocm.software/open-component-model/bindings/go/oci/spec/repository/path"
	ocirepov1 "ocm.software/open-component-model/bindings/go/oci/spec/repository/v1/oci"
	"ocm.software/open-component-model/bindings/go/runtime"

	apiv1alpha1 "github.com/openmcp-project/extensibility-utils/pkg/discovery/api/v1alpha1"
)

// mustRaw marshals an OCIImage access JSON into a *runtime.Raw for use in test resources.
func mustRaw(t *testing.T, imageRef string) *runtime.Raw {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"type":           "OCIImage",
		"imageReference": imageRef,
	})
	if err != nil {
		t.Fatalf("marshal access: %v", err)
	}
	raw := &runtime.Raw{}
	if err := json.Unmarshal(data, raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	return raw
}

// mustDescriptorJSON marshals a v2.Descriptor into a JSON blob for Status.Components.
func mustDescriptorJSON(t *testing.T, desc v2.Descriptor) []byte {
	t.Helper()
	data, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	return data
}

// mustLocalBlobRaw builds a *runtime.Raw for a LocalBlob access, optionally with a global OCIImage access.
func mustLocalBlobRaw(t *testing.T, localRef string, globalImageRef string) *runtime.Raw {
	if t != nil {
		t.Helper()
	}
	m := map[string]any{
		"type":           "LocalBlob/v1",
		"localReference": localRef,
		"mediaType":      "application/vnd.oci.image.manifest.v1+json",
	}
	if globalImageRef != "" {
		m["globalAccess"] = map[string]string{
			"type":           "OCIImage",
			"imageReference": globalImageRef,
		}
	}
	data, _ := json.Marshal(m)
	raw := &runtime.Raw{}
	_ = json.Unmarshal(data, raw)
	return raw
}

func TestComponentDescriptorBase(t *testing.T) {
	suffix := "/" + ocmpath.DefaultComponentDescriptorPath

	tests := []struct {
		name string
		repo ocirepov1.Repository
		want string
	}{
		{
			name: "plain host",
			repo: ocirepov1.Repository{BaseUrl: "ghcr.io"},
			want: "ghcr.io" + suffix,
		},
		{
			name: "host with https scheme",
			repo: ocirepov1.Repository{BaseUrl: "https://ghcr.io"},
			want: "ghcr.io" + suffix,
		},
		{
			name: "trailing slash stripped",
			repo: ocirepov1.Repository{BaseUrl: "ghcr.io/"},
			want: "ghcr.io" + suffix,
		},
		{
			name: "path in BaseUrl kept",
			repo: ocirepov1.Repository{BaseUrl: "ghcr.io/org/repo"},
			want: "ghcr.io/org/repo" + suffix,
		},
		{
			name: "explicit SubPath",
			repo: ocirepov1.Repository{BaseUrl: "ghcr.io", SubPath: "open-control-plane"},
			want: "ghcr.io/open-control-plane" + suffix,
		},
		{
			name: "scheme + path",
			repo: ocirepov1.Repository{BaseUrl: "https://ghcr.io/org/repo"},
			want: "ghcr.io/org/repo" + suffix,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := componentDescriptorBase(&tc.repo)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDiscovery(t *testing.T) {
	makeDesc := func(name, version string) v2.Descriptor {
		return v2.Descriptor{
			Component: v2.Component{
				ComponentMeta: v2.ComponentMeta{ObjectMeta: v2.ObjectMeta{Name: name, Version: version}},
			},
		}
	}

	tests := []struct {
		name       string
		components []apiextensionsv1.JSON
		wantCount  int
		wantErr    bool
	}{
		{
			name: "single component",
			components: []apiextensionsv1.JSON{
				{Raw: mustDescriptorJSON(t, makeDesc("ghcr.io/org/comp-a", "v1.0.0"))},
			},
			wantCount: 1,
		},
		{
			name: "multiple components",
			components: []apiextensionsv1.JSON{
				{Raw: mustDescriptorJSON(t, makeDesc("ghcr.io/org/comp-a", "v1.0.0"))},
				{Raw: mustDescriptorJSON(t, makeDesc("ghcr.io/org/comp-b", "v2.0.0"))},
			},
			wantCount: 2,
		},
		{
			name:       "invalid JSON returns error",
			components: []apiextensionsv1.JSON{{Raw: []byte("not-json")}},
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			disc := ocmv1alpha1.Discovery{
				Status: ocmv1alpha1.DiscoveryStatus{Components: tc.components},
			}
			components, err := parseDiscovery(disc)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(components) != tc.wantCount {
				t.Fatalf("want %d components, got %d", tc.wantCount, len(components))
			}
		})
	}
}

func TestFindComponent(t *testing.T) {
	label := func(name string) []v2.Label {
		v, _ := json.Marshal(name)
		return []v2.Label{{Name: "component", Value: v}}
	}
	matchLabel := func(name string) componentMatchFunc {
		v, _ := json.Marshal(name)
		return componentByLabel("component", v)
	}
	components := []Component{
		{Name: "ghcr.io/org/comp-a", Version: "v1.0.0", Labels: label("comp-a")},
		{Name: "ghcr.io/org/comp-a", Version: "v2.0.0", Labels: label("comp-a")},
		{Name: "ghcr.io/org/comp-b", Version: "v1.0.0", Labels: label("comp-b")},
	}

	tests := []struct {
		name      string
		matchFunc componentMatchFunc
		version   string
		wantName  string
		wantFound bool
	}{
		{
			name:      "nil matchFunc matches any component with version",
			matchFunc: nil,
			version:   "v1.0.0",
			wantName:  "ghcr.io/org/comp-a",
			wantFound: true,
		},
		{
			name:      "match by label and version",
			matchFunc: matchLabel("comp-b"),
			version:   "v1.0.0",
			wantName:  "ghcr.io/org/comp-b",
			wantFound: true,
		},
		{
			name:      "label matches multiple, version selects one",
			matchFunc: matchLabel("comp-a"),
			version:   "v2.0.0",
			wantName:  "ghcr.io/org/comp-a",
			wantFound: true,
		},
		{
			name:      "normalized matching",
			version:   "2.0.0",
			wantName:  "ghcr.io/org/comp-a",
			wantFound: true,
		},
		{
			name:      "version mismatch returns not found",
			matchFunc: matchLabel("comp-a"),
			version:   "v9.0.0",
			wantFound: false,
		},
		{
			name:      "no matching component",
			matchFunc: matchLabel("comp-missing"),
			version:   "v1.0.0",
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findComponent(components, tc.matchFunc, tc.version)
			if ok != tc.wantFound {
				t.Fatalf("found = %v, want %v", ok, tc.wantFound)
			}
			if ok && got.Name != tc.wantName {
				t.Errorf("got name %q, want %q", got.Name, tc.wantName)
			}
			if ok && normalizeVersion(got.Version) != normalizeVersion(tc.version) {
				t.Errorf("got version %q, want %q", got.Version, tc.version)
			}
		})
	}
}

func TestFindResource(t *testing.T) {
	imageResource := v2.Resource{
		ElementMeta: v2.ElementMeta{ObjectMeta: v2.ObjectMeta{Name: "test-image"}},
		Type:        "image",
		Access:      mustRaw(t, "ghcr.io/test-image:v1.0.0"),
	}
	component := Component{
		Name:      "ghcr.io/org/repo/comp",
		Resources: []v2.Resource{imageResource},
	}

	tests := []struct {
		name          string
		matchFunc     resourceMatchFunc
		wantAccessURL string
		wantErr       bool
	}{
		{
			name:    "nil matchFunc returns error",
			wantErr: true,
		},
		{
			name:          "matching resource by type",
			matchFunc:     resourceByType("image"),
			wantAccessURL: "ghcr.io/test-image:v1.0.0",
		},
		{
			name:          "matching resource by name",
			matchFunc:     resourceByName("test-image"),
			wantAccessURL: "ghcr.io/test-image:v1.0.0",
		},
		{
			name:      "no matching resource returns error",
			matchFunc: resourceByName("missing"),
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := findResource(component, "ghcr.io/org/repo", tc.matchFunc)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && res.AccessURL != tc.wantAccessURL {
				t.Errorf("got access URL %q, want %q", res.AccessURL, tc.wantAccessURL)
			}
		})
	}
}

func TestMatchFunctions(t *testing.T) {
	production, _ := json.Marshal("production")
	staging, _ := json.Marshal("staging")
	component := Component{
		Name:   "ghcr.io/org/comp-a",
		Labels: []v2.Label{{Name: "env", Value: production}},
	}
	resource := v2.Resource{
		ElementMeta: v2.ElementMeta{
			ObjectMeta: v2.ObjectMeta{
				Name:   "ghcr.io/org/repo/test-image",
				Labels: []v2.Label{{Name: "env", Value: production}},
			},
		},
		Type: "helmChart",
	}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"componentByLabel matching key and value", componentByLabel("env", production)(component), true},
		{"componentByLabel matching key wrong value", componentByLabel("env", staging)(component), false},
		{"componentByLabel missing key", componentByLabel("missing", production)(component), false},
		{"resourceByType matching", resourceByType("helmChart")(resource), true},
		{"resourceByType wrong", resourceByType("image")(resource), false},
		{"resourceByLabel matching key and value", resourceByLabel("env", production)(resource), true},
		{"resourceByLabel matching key wrong value", resourceByLabel("env", staging)(resource), false},
		{"resourceByLabel missing key", resourceByLabel("missing", production)(resource), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestDecodeAccess(t *testing.T) {
	const basePath = "ghcr.io/org/component-descriptors"
	const componentName = "github.com/org/comp"

	tests := []struct {
		name    string
		access  *runtime.Raw
		wantURL string
		wantErr bool
	}{
		{
			name:    "OCIImage returns image reference",
			access:  mustRaw(t, "ghcr.io/test-image:v1.0"),
			wantURL: "ghcr.io/test-image:v1.0",
		},
		{
			name:    "LocalBlob without GlobalAccess builds URL from basePath",
			access:  mustLocalBlobRaw(t, "sha256:abc123", ""),
			wantURL: basePath + "/" + componentName + "@sha256:abc123",
		},
		{
			name:    "LocalBlob with GlobalAccess returns global image reference",
			access:  mustLocalBlobRaw(t, "sha256:abc123", "ghcr.io/global:v1.0"),
			wantURL: "ghcr.io/global:v1.0",
		},
		{
			name:    "nil access returns error",
			access:  nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resource := v2.Resource{
				ElementMeta: v2.ElementMeta{ObjectMeta: v2.ObjectMeta{Name: "res"}},
				Access:      tc.access,
			}
			got, err := decodeAccess(resource, basePath, componentName)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.wantURL {
				t.Errorf("got %q, want %q", got, tc.wantURL)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		versions []string
		want     []string
	}{
		{
			name:     "orders by patch",
			versions: []string{"1.2.4", "1.2.3"},
			want:     []string{"1.2.3", "1.2.4"},
		},
		{
			name:     "multi-digit minor sorts semantically, not lexically",
			versions: []string{"1.10.0", "1.9.0", "1.2.0"},
			want:     []string{"1.2.0", "1.9.0", "1.10.0"},
		},
		{
			name:     "v prefix is tolerated",
			versions: []string{"v1.2.4", "1.2.3"},
			want:     []string{"1.2.3", "v1.2.4"},
		},
		{
			name:     "unparseable versions sort after valid ones lexicographically",
			versions: []string{"notaversion-b", "1.2.0", "notaversion-a"},
			want:     []string{"1.2.0", "notaversion-a", "notaversion-b"},
		},
		{
			name:     "mixed",
			versions: []string{"v1.10.0", "1.2.0", "notaversion-a", "1.9.0"},
			want:     []string{"1.2.0", "1.9.0", "v1.10.0", "notaversion-a"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sorted := slices.Clone(tc.versions)
			slices.SortFunc(sorted, CompareVersions)
			if !slices.Equal(sorted, tc.want) {
				t.Errorf("sorted = %v, want %v", sorted, tc.want)
			}
		})
	}
}

func TestDetermineResourceMatchFunc(t *testing.T) {
	labelValue := func(s string) json.RawMessage {
		v, _ := json.Marshal(s)
		return v
	}
	helmResource := v2.Resource{
		ElementMeta: v2.ElementMeta{ObjectMeta: v2.ObjectMeta{
			Name:   "crossplane-chart",
			Labels: []v2.Label{{Name: "env", Value: labelValue("production")}},
		}},
		Type: "helmChart",
	}
	imageResource := v2.Resource{
		ElementMeta: v2.ElementMeta{ObjectMeta: v2.ObjectMeta{Name: "crossplane-image"}},
		Type:        "image",
	}

	tests := []struct {
		name      string
		cfg       *apiv1alpha1.ResourceConfig
		requested string
		resource  v2.Resource
		wantMatch bool
		wantErr   bool
	}{
		{
			name:      "nil cfg matches by requested type",
			cfg:       nil,
			requested: "helmChart",
			resource:  helmResource,
			wantMatch: true,
		},
		{
			name:      "nil cfg rejects other type",
			cfg:       nil,
			requested: "helmChart",
			resource:  imageResource,
			wantMatch: false,
		},
		{
			name:      "name cfg matches by name ignoring requested type",
			cfg:       &apiv1alpha1.ResourceConfig{Name: "crossplane-chart"},
			requested: "image",
			resource:  helmResource,
			wantMatch: true,
		},
		{
			name:      "name cfg rejects other name",
			cfg:       &apiv1alpha1.ResourceConfig{Name: "crossplane-chart"},
			requested: "image",
			resource:  imageResource,
			wantMatch: false,
		},
		{
			name:      "resourceType cfg matches by type ignoring requested",
			cfg:       &apiv1alpha1.ResourceConfig{ResourceType: "helmChart"},
			requested: "chart",
			resource:  helmResource,
			wantMatch: true,
		},
		{
			name:      "resourceType cfg rejects other type",
			cfg:       &apiv1alpha1.ResourceConfig{ResourceType: "helmChart"},
			requested: "chart",
			resource:  imageResource,
			wantMatch: false,
		},
		{
			name:      "label cfg matches by label",
			cfg:       &apiv1alpha1.ResourceConfig{Label: &apiv1alpha1.LabelConfig{Key: "env", Value: "production"}},
			resource:  helmResource,
			wantMatch: true,
		},
		{
			name:      "label cfg rejects resource without label",
			cfg:       &apiv1alpha1.ResourceConfig{Label: &apiv1alpha1.LabelConfig{Key: "env", Value: "production"}},
			resource:  imageResource,
			wantMatch: false,
		},
		{
			name:    "label cfg with empty key errors",
			cfg:     &apiv1alpha1.ResourceConfig{Label: &apiv1alpha1.LabelConfig{Key: "", Value: "production"}},
			wantErr: true,
		},
		{
			name:    "label cfg with empty value errors",
			cfg:     &apiv1alpha1.ResourceConfig{Label: &apiv1alpha1.LabelConfig{Key: "env", Value: ""}},
			wantErr: true,
		},
		{
			name:    "empty cfg errors",
			cfg:     &apiv1alpha1.ResourceConfig{},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rmf, err := determineResourceMatchFunc(tc.cfg, tc.requested)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if rmf != nil {
					t.Errorf("expected nil matcher on error, got non-nil")
				}
				return
			}
			if got := rmf(tc.resource); got != tc.wantMatch {
				t.Errorf("match = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}

func TestComponentMatchFunc(t *testing.T) {
	r := &resolver{serviceProvider: "sp-test"}
	defaultKey := defaultComponentLabel("sp-test")

	labelled := func(key, value string) Component {
		v, _ := json.Marshal(value)
		return Component{Labels: []v2.Label{{Name: key, Value: v}}}
	}

	tests := []struct {
		name       string
		discConfig apiv1alpha1.DiscoveryConfig
		component  string
		target     Component
		wantNil    bool
		wantMatch  bool
	}{
		{
			name:      "empty component returns nil matcher",
			component: "",
			wantNil:   true,
		},
		{
			name:      "default label key matches component",
			component: "provider-btp",
			target:    labelled(defaultKey, "provider-btp"),
			wantMatch: true,
		},
		{
			name:      "default label key rejects other component",
			component: "provider-btp",
			target:    labelled(defaultKey, "provider-kubernetes"),
			wantMatch: false,
		},
		{
			name:       "custom label key matches component",
			discConfig: apiv1alpha1.DiscoveryConfig{ComponentMatchLabelKey: "open-control-plane.io/name"},
			component:  "provider-btp",
			target:     labelled("custom.io/name", "provider-btp"),
			wantMatch:  true,
		},
		{
			name:       "custom label key ignores default key",
			discConfig: apiv1alpha1.DiscoveryConfig{ComponentMatchLabelKey: "open-control-plane.io/name"},
			component:  "provider-btp",
			target:     labelled(defaultKey, "provider-btp"),
			wantMatch:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmf, err := r.componentMatchFunc(tc.discConfig, ResolveOptions{ComponentLabel: tc.component})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if cmf != nil {
					t.Errorf("expected nil matcher, got non-nil")
				}
				return
			}
			if cmf == nil {
				t.Fatalf("expected non-nil matcher")
			}
			if got := cmf(tc.target); got != tc.wantMatch {
				t.Errorf("match = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}

func TestOCIRepoFromComponent(t *testing.T) {
	rawJSON := func(m map[string]any) []byte {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal repositorySpec: %v", err)
		}
		return data
	}
	component := func(raw []byte) *ocmv1alpha1.Component {
		var spec *apiextensionsv1.JSON
		if raw != nil {
			spec = &apiextensionsv1.JSON{Raw: raw}
		}
		return &ocmv1alpha1.Component{
			Status: ocmv1alpha1.ComponentStatus{
				Component: ocmv1alpha1.ComponentInfo{RepositorySpec: spec},
			},
		}
	}

	tests := []struct {
		name        string
		component   *ocmv1alpha1.Component
		wantBaseUrl string
		wantErr     bool
	}{
		{
			name:      "missing repositorySpec errors",
			component: component(nil),
			wantErr:   true,
		},
		{
			name:      "invalid JSON errors",
			component: component([]byte("not-json")),
			wantErr:   true,
		},
		{
			name:      "unregistered type errors",
			component: component(rawJSON(map[string]any{"type": "UnregisteredTypeRepository", "baseUrl": "ghcr.io/org"})),
			wantErr:   true,
		},
		{
			name:        "valid OCIRepository returns baseURL",
			component:   component(rawJSON(map[string]any{"type": "OCIRepository", "baseUrl": "ghcr.io/org"})),
			wantBaseUrl: "ghcr.io/org",
		},
		{
			name:        "legacy OCIRegistry type is accepted",
			component:   component(rawJSON(map[string]any{"type": "OCIRegistry", "baseUrl": "ghcr.io/legacy"})),
			wantBaseUrl: "ghcr.io/legacy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := ociRepoFromComponent(tc.component)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if repo.BaseUrl != tc.wantBaseUrl {
				t.Errorf("BaseUrl = %q, want %q", repo.BaseUrl, tc.wantBaseUrl)
			}
		})
	}
}
