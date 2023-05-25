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

Migrations to date have existed as a function that is called during Auth initialization. The migration function is left as is for a release or two and is then deleted. This makes tracking when and which migrations have been applied difficult and prevents skipping any major versions when upgrading since there are no guarantees that the latest version will include all the migrations between versions.

## Details

### When is a Migration required?

Migrations are not needed for every change to a backend resource. Adding a new field to the resource should be a backward compatible operation by nature of Protocol Buffers and does not require an actual migration. Any major changes to a resource that alter its shape, change its encoding, or moving a single resource into several other resources are candidates for a migration.

### Persistent Migrations

All new migrations MUST exist in perpetuity to allow for the correct migrations to be applied regardless of which version of Teleport is being upgraded from and to. Migrations MUST be numbered and applied in sequence. The order of migrations MUST not change and a migration MUST not be altered once it has been included in a release.

To make tracking and discovering migrations easier all migrations MUST be placed in `lib/auth/migrations`. Each migration should be named `$number-migration-description.go` and contain a single migration function, see the Migrations section below for details. The following is an example of four migrations:

```
./lib/auth/migrations/
├── 0001-initial_migration.go
├── 0002-another_migration.go
├── 0003-some_other_migration.go
└── 0004-yet_another_migration.go
```

### Migration History

Migration history is to be stored in the backend to enable tracking which migrations have been applied, when, and whether they were successful or not.
The key `/migrations/<migration_number>` will store the history of a migration.

Cluster admins may view the migration history via `tctl migrations ls`.

### Migrating Keys

All migrations MUST not be performed in place, instead migrations MUST migrate both the keys and values in the backend.
For example, to migrate the data stored in key `some/path/to/resource/<ID>` we must leave the original value at `some/path/to/resource/<ID>` as is and write the migrated value to `some/new/path/to/resource/<ID>`.
This allows older versions of Teleport to still operate on `some/path/to/resource/<ID>`, while newer versions can first attempt reads from `some/new/path/to/resource/<ID>`, and fall back to reading from `some/path/to/resource/<ID>` if the migrated key does not exist yet.

Migrations MUST also not delete the key being migrated for at least one major version of Teleport. This allows users to upgrade to and downgrade from a single major version, but prevents any downgrading beyond that.
Continuing from the example above, if `some/path/to/resource/<ID>` is migrated to `some/new/path/to/resource/<ID>` in v1.0.0 a follow up migration should be added in v2.0.0 to delete the keys under `some/path/to/resource` that were migrated in v1.0.0.

Keys should be versioned to determine which version of a resource exists at any given key range. A key prefix of `/type/version/subkind/name` should be used where possible to have uniformity.
For example if nodes were migrated from `/nodes/default/<UUID>` it should be to `/nodes/v2/default/<UUID>`.

### Backward Compatibility

Migrations have historically been backward incompatible operations. Migrations altered the data in place without changing the key, which can prevent any versions prior to the migration from being able to unmarshal the value into the resource representation. The only way to downgrade in this scenario was to restore the backend from a backup prior to the migration, attempt to manually rollback the migration, or deleting the entire key range that was migrated. Moving forward it will be possible to downgrade a single major version without being impacted by a migration.

### Testing Migrations

While the framework laid out in this RFD allows migrations to be applied in a deterministic manner, it does not provide a uniform rule or process for any code that is impacted by a migration. To ensure that a migration is functional testing should consider a wide range of simultaneous versions in a cluster in accordance to our version compatibility matrix. Imagine that we are going to introduce a migration in v3.0.0, we must test the following for an extended period of time(10m) to ensure all supported versions are functional:

| Auth 1 | Auth 2  | Proxy   | Agents  |
| ------ | ------- | ------- | ------- |
| v3.0.0 | v3.0.0  | v3.0.0  | v3.0.0  |
| v3.0.0 | <v3.0.0 | <v3.0.0 | <v3.0.0 |
| v3.0.0 | v3.0.0  | <v3.0.0 | <v3.0.0 |
| v3.0.0 | v3.0.0  | v3.0.0  | <v3.0.0 |

Testing multiple versions of Auth at the same time will help validate that the migration is backward compatible and that a rollback is possible. Ensuring that Auth running with the migration and all other instances without the migration is also crucial to test since Auth is always the first component updated. If the migration is unknown by the agents it should not impact their ability to operate.

It can also be a worthwhile exercise to run through the same testing matrix above for any backend changes that require a full blown migration. Even adding a new field to an existing resource can have [drastic consequences](https://github.com/gravitational/teleport/issues/25644) if the previous version cannot unmarshal the unknown field. A mixed fleet with agents running an older version of Teleport than Auth can also result in undefined behavior new fields in a resource have an impact on business logic.

### Implementation Details

#### Auth Initialization

Every time Auth starts it will evaluate all known migrations against the cluster migration history and apply any outstanding migrations. This will allow clusters to skip major versions when upgrading and still have the correct migrations applied in order.
However, due to the fact that migration history is not persisted today, upgrades must be done in sequence up until the very first release that contains the changes outlined in this RFD.

All migrations will be performed in a background goroutine that is spawned from `auth.Init` to prevent them from blocking Auth initialization. For each migration that needs to be applied Auth will attempt to acquire the lock `/migrations/lock/<migration_number>` for the duration of that migration execution to prevent simultaneous instances from running the same migration. When Auth acquires the lock it must check the status of the migration to determine if that migration was already completed by another instance, if it was then no action is to be taken. If another instance of Auth attempted the migration but failed, then the next instance that gets the lock should attempt the migration. After successfully applying a migration Auth will store that migrations status in the backend and then move on to the next migration until none remain, repeating the same process for each migration.

In the event that all Auth instances fail to complete a single migration, no further migrations may be applied, and the migration routine MUST exit. Logs should be emitted that indicate why the migration failed, the `teleport_migrations` metric should indicate a failure to allow admins to enhance obersvability. Cluster admins can also interrogate the migrations via `tctl migrations ls` to see any errors associated with a particular migration. After the issue is resolved through manual intervention `tctl migrations apply` can be invoked to kick off the migration process mentioned above.

#### Migrations

Each migration MUST be a `MigrationFunc` which will be able to interact with the `backend.Backend` to perform the required migration. If the migration is completed successfully then `nil` should be returned otherwise an error that indicates what went wrong should be returned.

```go
type MigrationFunc func(ctx context.Context, b backend.Backend) error
```

A migration that converts nodes to be stored in binary proto instead of json might look like the following:

```go
// exampleMigration converts the encoding used to store [types.Server]
// from json to proto.
func exampleMigration(ctx context.Context, b backend.Backend) error {
	oldSvc, err := generic.NewService(&generic.ServiceConfig[types.Server]{
		Backend:       b,
		ResourceKind:  types.KindNode,
		BackendPrefix: backend.Key(nodesPrefix, namespace),
		MarshalFunc:   services.MarshalServer,
		UnmarshalFunc: services.UnmarshalServer,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	newSvc, err := generic.NewService(&generic.ServiceConfig[types.Server]{
		Backend:       b,
		ResourceKind:  types.KindNode,
		BackendPrefix: backend.Key(nodesPrefixV2, namespace),
		MarshalFunc:   func(t types.Server) ([]byte, error) {
			return proto.Marshal(t)
		}
		UnmarshalFunc: func(types.Server) ([]byte, error) { return nil, nil }
	})
	if err != nil {
		return trace.Wrap(err)
	}

	servers, err := oldSvc.GetResources(ctx)
	if err != nil {
		if trace.IsNotFound(err) {
			return nil
		}
		return trace.Wrap(err)
	}

	for _, s := range servers {
		if err := newSvc.CreateResource(ctx, s); err != nil {
			return trace.Wrap(err)
		}
	}

	return nil
}
```

#### Migration Backend Service

Now that there is a generic backend service implementation we can provide a single implementation that attempts to reads from the new key and falling back to the previous key if not found. Whenever a migration occurs for a particular resource only the backend service would need to be converted to the generic service which already implemented the fallback mechanism.

We can either extend the `generic.Service` with an optional `PreviousBackendPrefix`, `MarshalFunc`, `UnmarshalFunc` or add a new `MigrationService` that has a `generic.Service` for each `BackendPrefix` and marshal function pair like the following:

```go
type MigrationService[T types.Resource] struct {
        previous *generic.Service[T]
        current  *generic.Service[T]
}
```

Retrieving a resource could be implemented as so:

```go
func (s *MigrationService[T]) GetResource(ctx context.Context, name string) (T, error) {
        t, err := s.current.GetResource(ctx, name)
        switch {
        case err == nil:
                return t, nil
        case trace.IsNotFound(err):
                if t, err := s.previous.GetResource(ctx, name); err == nil {
                        return t, nil
                }

                return t, trace.Wrap(err)
        default:
                return t, trace.Wrap(err)
        }
}
```

Listing across both keys can be achieved using some of the [stream helpers](https://github.com/gravitational/teleport/tree/fspmarshall/sorted-stream-helpers) that were originally included in https://github.com/gravitational/teleport/pull/18361 but were not included in the final version of that PR since nothing was consuming them yet.

### Security

Migrations already exist today, this RFD only proposes a way to make them deterministic and elevates visibility into migration history. Only users with the correct permissions will be able to invoke `tctl migrations apply` and in most cases the command will result in a no-op. If all migrations have already been applied nothing will be done. We will also only allow a single migration process to be in flight at any given time.

### UX

Cluster admins will have a much simpler and straightforward upgrade procedure to follow which should help reduce some of the support load. Performing migrations in a way that eliminates the need to change the number of Auth replicas should also result in a more stable cluster during and after upgrades.

This should have the biggest impact on Cloud tenants that experience some outages during upgrades. By allowing multiple instances of Auth to exist during an upgrade event we will be able to reduce downtime experienced by users.

`tctl migrations ls` and `tctl migrations apply` will be added to allow admins to inspect the status of the migrations and to retry applying migrations in the event that one fails.

### Proto Specification

```proto
// MigrationService provides methods to view migration history and rerun any
// failed migrations without having to restart Auth.
service MigrationService {
  // ListMigrations returns the migration history of the cluster.
  rpc ListMigrations(ListMigrationsRequest) returns (ListMigrationssResponse);
  // ApplyMigrations causes migrations to be applied. If all migrations are
  // applied this is a no-op. This should only be used to cause migrations to
  // be rerun after resolving any issues that occurred during an automatic
  // migration from Auth initialization.
  rpc ApplyMigrations(ApplyMigrationsRequest) returns (google.protobuf.Empty);
}


// Request for ListMigrations.
//
// Follows the pagination semantics of
// https://cloud.google.com/apis/design/standard_methods#list.
message ListMigrationsRequest {
  // The maximum number of items to return.
  // The server may impose a different page size at its discretion.
  int32 page_size = 1;

  // The next_page_token value returned from a previous ListMigrationsRequest, if any.
  string page_token = 2;
}

// Response for ListMigrations.
message ListMigrationssResponse {
  // A batch of migrations from the request range.
  repeated Migration migrations = 1;

  // Token to retrieve the next page of results, or empty if there are no
  // more results in the list.
  string next_page_token = 2;
}

// Migration represents the status of a backend migration.
message Migration {
  // Kind is the resource kind
  string kind = 1;
  // SubKind is an optional resource subkind
  string sub_kind = 2;
  // Version is version
  string version = 3;
  // Metadata is resource metadata
  Metadata metadata = 4;

  // The number of the migration.
  number int = 5;
  // The timestamp that the migration was applied on.
  google.protobuf.Timestamp applied_at = 6;
  // The time it took to apply the migration.
  google.protobuf.Duration execution_time = 7;
  // Whether the outcome of the migration was successful.
  bool success = 8;
  // A friendly message that describes the output of the migration.
  // If the migration failed it will contain the error, if the migration
  // was applied cleanly it will contain "success".
  string message = 9;
}
```
