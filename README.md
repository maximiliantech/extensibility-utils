# Extensibility Utils

A Go utility library for simplifying flux based OpenControlPlane service-providers with cross-cluster object management and Flux integration.

## Features

- **Object Manager**: Reconcile and manage Kubernetes objects across one or more clusters
- **Flux Integration**: Deploy Helm charts through Flux HelmRelease and OCIRepository resources
- **Secret Management**: Copy and manage Kubernetes secrets across clusters
- **Lifecycle Tracking**: Monitor object status and readiness with configurable policies
- **Dependency Management**: Define and respect object dependencies during reconciliation

## Package Documentation

### `pkg/objectmanager`

Core package for managing Kubernetes objects across clusters.

**Key Types:**
- `Manager`: Main interface for managing object lifecycle (Apply, Delete)
- `Cluster`: Represents a Kubernetes cluster containing objects
- `Object`: Wraps a client.Object with reconciliation and status functions
- `Cleaner`: Removes managed objects not in a specified list

**Key Features:**
- Dependency-aware reconciliation
- Configurable deletion policies (delete or orphan)
- Status tracking (Ready, Progressing, Terminating, Unknown)
- Multiple cleaner support for cleanup operations

### `pkg/flux`

Provides helpers for deploying Helm charts through Flux.

**Key Functions:**
- `ManageHelmRelease()`: Function to register a HelmRelease with a OCIRepository source

### `pkg/secret`

Utilities for managing Kubernetes secrets across clusters.

**Key Functions:**
- `ManagePullSecret()`: Register an image-pull secret copy
- `NewCleaner()`: Create a cleaner for managed pull secrets
- `PrefixName()`: Prefix and validate secret names to Kubernetes limits

### `pkg/discovery`

Utilities to resolve OCM (Open Component Model) components and resources from `Discovery` objects on a release channel umbrella component.

**Key Types:**
- `Resolver`: Main interface for managing `Discovery` objects and resolving resources (Apply, Credentials, Resolve, ListComponentVersions)
- `Config`: Configuration for `NewResolver` (service provider, namespace, base component, discovery configs)
- `Component`: A parsed OCM component descriptor entry (name, version, resources, labels)
- `Resource`: A resolved OCM resource with a fully-qualified OCI access URL

**Resolver Methods:**
- `Apply()`: Create or update the `Discovery` objects on the platform cluster, returning true if a requeue is needed
- `Delete()`: Delete the `Discovery` objects on the platform cluster, returning true if a requeue is needed
- `Credentials()`: Return a reference to the first docker-config secret in the base component's effective OCM config, or nil if none exists
- `Resolve()`: Fetch the `Discovery` for a given id, find the component matching the version (optionally restricted via `WithComponentLabel`), then return the access URL of the first resource matching the `resourceIdentifier`
- `ListComponentVersions()`: Return the available component versions for a given id (optionally restricted via `WithComponentLabel`), sorted oldest first

A `fake.Resolver` (`pkg/discovery/fake`) is provided for testing consumers of the `Resolver` interface.

#### Usage

**1. Create a resolver**

`NewResolver` takes a `Config` whose central field is the per-discovery `DiscoveryConfig` map. It configures how each `Discovery` object is laid out and how its components and resources are matched. Creating the resolver also fetches the base (umbrella) component and computes the OCI base path shared by all resolved resources; it fails if the base component cannot be found.

**2. Apply/Delete the Discovery objects**

`Apply` creates or updates the `Discovery` objects on the platform cluster; `Delete` removes them. Both return a `requeue` bool: `true` means the objects are not settled yet and reconciliation should run again. Resolve only works once the relevant `Discovery` is applied and ready.

**3. Resolve resources**

`Resolve` returns the OCI access URL of a single resource from the components in a discovery. `ListComponentVersions` returns the matching components' versions, sorted oldest first; it uses the same component matching as `Resolve`.

`Credentials` returns a reference to the first `kubernetes.io/dockerconfigjson` secret listed in the base component's effective OCM config (looked up in each entry's namespace, falling back to the resolver's namespace). It returns `nil` when no such secret exists, and only returns an error if it never found one *and* at least one lookup failed along the way.

#### How matching works

Resolution happens in two stages: first a component is selected within the discovery, then a resource is selected within that component.

**Component matching**

By default components are matched by `version` alone. If the version is not enough to identify a single component, pass `WithComponentLabel(value)` to `Resolve` / `ListComponentVersions`. The `value` is then matched against a component label:

| Label key used | When |
| --- | --- |
| `<serviceProvider>.services.open-control-plane.io/component` | default |
| `DiscoveryConfig.ComponentMatchLabelKey` | when set for that discovery |

**Resource matching**

The `resourceIdentifier` you pass to `Resolve` (e.g. `"image"`, `"helmChart"`) is, by default, matched against each resource's **type**. To change what it matches against, add an entry under `DiscoveryConfig.ResourceConfig` keyed by that identifier. Each entry sets exactly one of the following:

| `ResourceConfig[identifier]` field | Resource is matched by |
| --- | --- |
| *(no entry)* | resource **type** equals the identifier |
| `name` | resource **name** equals this value |
| `resourceType` | resource **type** equals this value |
| `label` | resource has a label with this `key` and `value` |

The map key is the identifier you will pass to `Resolve`. It is only a lookup handle to find the applicable config and is *not* itself compared against anything on the resource. The chosen field then decides what the resource is actually matched against. This lets a caller-facing identifier differ from how the resource is named or typed in the component.

For example, given this `DiscoveryConfig`:

```go
apiv1alpha1.DiscoveryConfig{
    ResourceConfig: map[string]apiv1alpha1.ResourceConfig{
        // Resolve(ctx, discID, "image", version) matches the resource whose type == "ociImage"
        "image": {ResourceType: "ociImage"},

        // Resolve(ctx, discID, "chart", version) matches the resource whose name == "my-helm-chart"
        "chart": {Name: "my-helm-chart"},

        // Resolve(ctx, discID, "operator", version) matches the resource carrying
        // the label role=operator
        "operator": {Label: &apiv1alpha1.LabelConfig{Key: "role", Value: "operator"}},
    },
}
```

- `Resolve(ctx, discID, "image", version)` → first resource with `type == "ociImage"`.
- `Resolve(ctx, discID, "chart", version)` → first resource with `name == "my-helm-chart"`.
- `Resolve(ctx, discID, "operator", version)` → first resource with a label `role` whose value is `operator`.
- `Resolve(ctx, discID, "helmChart", version)` → no entry for `"helmChart"`, so it falls back to matching `type == "helmChart"`.

Note that `name`, `resourceType`, and `label` are mutually exclusive per entry: the config is validated to carry exactly one of them, and an entry with a `label` missing its `key` or `value` is rejected.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/openmcp-project/repository-template/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](https://github.com/openmcp-project/.github/blob/main/CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/openmcp-project/repository-template/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/openmcp-project/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright OpenControlPlane contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/openmcp-project/repository-template).

---

<p align="center">
  <a href="https://apeirora.eu/content/projects/">
    <img alt="BMWK-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="300"/>
  </a>
</p>

<p align="center">
  OpenControlPlane is part of <a href="https://apeirora.eu/content/projects/">ApeiroRA</a>, an EU Important Project of Common European Interest (IPCEI-CIS).
</p>

<p align="center">
  Copyright Linux Foundation Europe. For web site terms of use, trademark policy and other project policies please see <a href="https://linuxfoundation.eu/en/policies">https://linuxfoundation.eu/en/policies</a>.
</p>
