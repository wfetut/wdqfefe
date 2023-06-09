---
authors: Tim Ross (tim.ross@goteleport.com)
state: draft
---

# RFD 126 - Backend Migrations

## Required Approvers

- Engineering @zmb3 && (@fspmarshall || @espadollini )
- Product: ( @klizhentas || @russjones )

## What

A more reliable and robust mechanism to perform backend migrations.

## Why

The upgrade process requires scaling Auth down to a single instance to ensure that migrations are only performed once as well as preventing a new version and an old version from operating on the same keys with different schema. This is cumbersome and makes the upgrade process a pain point for cluster admins. Scaling Auth down and back up also results in an [uneven load](https://github.com/gravitational/teleport/issues/7029) which can cause connectivity issues and backend latency that can result in a thundering herd.

## Details

#### In Place Migrations

Migrations have historically occurred in place via an explicit migration step added to Auth initialization or by modifying the protobuf message of the backend resource. Below is a migration that was added in [#3161](https://github.com/gravitational/teleport/pull/3161) to ensure that the `BPF` role option added in that PR would have a default value set for any existing roles.

```go
// migrateRoleOptions adds the "enhanced_recording" option to all roles.
func migrateRoleOptions(asrv *AuthServer) error {
	roles, err := asrv.GetRoles()
	if err != nil {
		return trace.Wrap(err)
	}

	for _, role := range roles {
		options := role.GetOptions()
		if options.BPF == nil {
			fmt.Printf("--> Migrating role %v. Added default enhanced events.", role.GetName())
			log.Debugf("Migrating role %v. Added default enhanced events.", role.GetName())
			options.BPF = defaults.EnhancedEvents()
		} else {
			continue
		}
		role.SetOptions(options)
		err := asrv.UpsertRole(role)
		if err != nil {
			return trace.Wrap(err)
		}
	}

	return nil
}
```

Explicit migrations like `migrateRoleOptions` that are run during Auth initialization pose several issues:
1) They prevent Auth from starting and being able to serve requests which causes downtime for a cluster.
2) They must be coordinated with any other Auth instances to ensure that only a single Auth instance performs the migration. All other instances of Auth should be prevented from starting to avoid interfering with the migrations.
3) Migrations are run every time Auth starts until the migration is deleted in a future version of Teleport, even though they may be a no-op after the first time that they are applied.
4) Migrations do not prevent migrated data from being overwritten by older instances of Auth if the migrations are performed on an existing key range instead of to a new key range.

The migration from `#3161` could have omitted `migrateRoleOptions` entirely and relied on lazily migrating roles to set a default `BPF` option for any existing roles. In fact, `#3161` updated [`CheckAndSetDefaults`](https://github.com/gravitational/teleport/pull/3161/files#diff-9b3fca74b2b2d4ce46a466de333f6c3e7640c21da8bd587e1837aa7a113eb7e7R619-R621) to do just that:

```go
func (r *RoleV3) CheckAndSetDefaults() error {
	...
	if len(r.Spec.Options.BPF) == 0 {
		r.Spec.Options.BPF = defaults.EnhancedEvents()
	}
	...
}
```

Any role without a `BPF` option set would have the default value applied on each read from the backend. Subsequent writes of the role would then include the default, or any explicitly set `BPF` option and thus the role would eventually be migrated.

While lazy migrations do eliminate problems 1 and 2 from above, they still don't totally help solve 3 or 4. Since the lazy migration doesn't occur during initialization they partially help with 3, yet they must still exist for the same period of time as the explicit migration.

Neither approach helps to address having multiple version of Auth running at the same time, one that know about the migration, and one that does not from stomping on data written by the other. Imagine the following scenario where `Auth-v2` and `Auth-v1` are running at the same time, but only `Auth-v2` is aware that a field in `types.Role` was modified:

```mermaid
Alice->>+Auth-v2: GetRole
Auth-v2-->>Alice: Here's RoleA
Alice->>+Auth-v2: UpsertRole
Auth-v2-->>Alice: Upserted!
Alice->>+Auth-v1: GetRole
Auth-v1-->>Alice: Here's RoleA
Alice->>+Auth-v1: UpsertRole
Auth-v1-->>Alice: Upserted!
Alice->>+Auth-v2: GetRole
Auth-v2-->>Alice: Here's RoleA
```

The second upsert of `RoleA` performed by `Auth-v1` will drop any new fields that were added by `Auth-v2` which will produce a different value of the final version of `RoleA` than what Alice would expect to see. The backend stores a json encoded version of `RoleA`, when `Auth-v1` reads `RoleA` after `Auth-v2` may have written values for a new field it will ignore them because it doesn't know they exist. When `Auth-v1` stores the role again the new fields will be completely dropped.

There are a few potential solutions to this problem:

#### Option 1: Read Only Replicas
When an Auth server detects that there are newer versions of Auth present in the cluster it can turn itself into a read only replica. Rejecting any write requests prevents data in the backend from becoming corrupt at the expense of availability. In this scenario multiple different version of Auth can coexist, however it likely will not result in any less downtime than using a recreate deployment strategy.

Detection of a new Auth server may also take some time and not be a reliable picture of the cluster if heartbeats are stale, or a new Auth server was only online long enough to heartbeat and then was terminated. Without an Auth peering mechanism detection of different Auth instances within a cluster may not be reliable.

This method does not guarantee backward compatability either. The new version of Auth may alter backend data in such a way that the previous version cannot comprehend it. In the event that the new version is rolled back and the old version is no longer a read only replica it may still leave the cluster in an unusable state.

#### Option 2: Delay migrations
Auth could try to detect there are any older versions of Auth present in the cluster and delay applying and migrations until they no longer exist. If Auth is able to process requests before migrations are applied, it would also have to reject any write which contained a new field that the older version of Auth did not know about.

While this does lead to less potential downtime than Option 1, it suffers from the same detection and backward incompatability problems as Option 1. It can also cause some confusing user experience if attempting to use new features may be denied for some unknown period of time until all migrations are able to be applied.

#### Option 3: Leverage Resource Versioning
If all resources were properly versioned and clients were explicitly able to indicate which version of the resource they were reading/writing we could potentially be able to apply the best of both Option 1 and Option 2.

Write requests with a known resource version are to be processed and persisted in the backend. Auth must not process a write request with a newer resource version than it knows about to prevent losing data.

Any read requests for a resource version of the same version as stored in the backend returned the resource from the backend unmodified. Read requests for an older version then the currently stored resource version must be converted into the older version. Read requests for a version of a resource that is newer than the stored version of the resource are rejected and result in an error returned to the caller.

This scenario is still not perfect and may result in some read/write operations being rejected by Auth. However, compared to Option 1 and Option 2 the number of operations rejected will be limited to only those trying to use migrated resources.

#### Option 4: Phased Migration
We can separate any changes to resources and the business logic that relies on the new representation into subsequent releases of Teleport. If the first release solely included backend resource changes and a mechanism to convert resources between versions we wouldn't have a way to write any data that could get overwritten by the previous version since there was no business logic that would know about the new version yet. The second release could then start to use the new resource representation without fear of the previous version causing conflicting versions of a stored resource. Until both Auth instances were upgraded to the new version there is still the posibility that the new application logic may not be present to process client requests. However, since Auth is always supposed to be the most recent instance of Teleport in the cluster this should be expected.

This approach is better suited for Cloud when we get to the point of updating more frequently with smaller change sets. Since the cloud stable release channel should always lag behind the Auth version we shouldn't have to worry as much about missing application logic when upgrading to the second phase with this strategy. We could also ensure that both phase one and phase two end up in the same major release to allow self hosted users following our current upgrade strategy from being impacted. To ensure that this approach doesn't cause a delay in features being available for Cloud users this would also work best in a world where we deployed to Cloud much more frequently.

The biggest shift with this approach will be in our developer experience. When implementing a new feature that requires a migration it is imperative to not include any application logic that relies on the new resource representation in the same release.


### Key Migrations

Any larger changes to backend resources should not happen in place, instead a new key range should be used and the old key range should remain unmodified. Major changes to a resource that alter its shape, change its encoding, or moving a single resource into several other resources are candidates for a key migration.

For example, to migrate the data stored in key `some/path/to/resource/<ID>` we must leave the original value at `some/path/to/resource/<ID>` as is and write the migrated value to `some/new/path/to/resource/<ID>`.
This allows older versions of Teleport to still operate on `some/path/to/resource/<ID>`, while newer versions can first attempt reads from `some/new/path/to/resource/<ID>`, and fall back to reading from `some/path/to/resource/<ID>` if the migrated key does not exist yet.

These migrations MUST also not delete the key being migrated in the same migration. This allows users to upgrade to and downgrade from a version that contains a larger migration. Continuing from the example above, if `some/path/to/resource/<ID>` is migrated to `some/new/path/to/resource/<ID>` in v1.0.0 a follow up migration should be added in v2.0.0 to delete the keys under `some/path/to/resource` that were migrated in v1.0.0.

Keys should be versioned to determine which version of a resource exists at any given key range. A key prefix of `/type/version/subkind/name` should be used where possible to have uniformity.
For example if nodes were migrated from `/nodes/default/<UUID>` it should be to `/nodes/v2/default/<UUID>`.


### Testing Migrations

The framework laid out in this RFD does not provide a uniform rule or process for any code that is impacted by a migration. To ensure that a migration is functional testing should consider a wide range of simultaneous versions in a cluster in accordance to our version compatibility matrix. Imagine that we are going to introduce a migration in v3.0.0, we must test the following for an extended period of time(10m) to ensure all supported versions are functional:

| Auth 1 | Auth 2  | Proxy   | Agents  |
| ------ | ------- | ------- | ------- |
| v3.0.0 | v3.0.0  | v3.0.0  | v3.0.0  |
| v3.0.0 | <v3.0.0 | <v3.0.0 | <v3.0.0 |
| v3.0.0 | v3.0.0  | <v3.0.0 | <v3.0.0 |
| v3.0.0 | v3.0.0  | v3.0.0  | <v3.0.0 |

Testing multiple versions of Auth at the same time will help validate that the migration is backward compatible and that a rollback is possible. Ensuring that Auth running with the migration and all other instances without the migration is also crucial to test since Auth is always the first component updated. If the migration is unknown by the agents it should not impact their ability to operate.


### Security

Migrations already exist today and cannot be triggered by a malicious actor.


### UX

Cluster admins will have a much simpler and straightforward upgrade procedure to follow which should help reduce some of the support load. Performing migrations in a way that eliminates the need to change the number of Auth replicas should also result in a more stable cluster during and after upgrades.

This should have the biggest impact on Cloud tenants that experience some outages during upgrades. By allowing multiple instances of Auth to exist during an upgrade event we will be able to reduce downtime experienced by users.
