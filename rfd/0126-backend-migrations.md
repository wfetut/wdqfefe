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

The upgrade process requires scaling Auth down to a single instance to ensure that migrations are only performed once as
well as preventing a new version and an old version from operating on the same keys with different schema. This is
cumbersome and makes the upgrade process a pain point for cluster admins. Scaling Auth down and back up also results in
an [uneven load](https://github.com/gravitational/teleport/issues/7029) which can cause connectivity issues and backend
latency that can result in a thundering herd.

Migrations to date have existed as a function that is called during Auth initialization. The migration function is left
as is for a release or two and is then deleted. This makes tracking when and which migrations have been applied
difficult and prevents skipping any major versions when upgrading since there are no guarantees that the latest version
will include all the migrations between versions. This also prevents Auth from serving any requests until after all
migrations have been completed which causes downtime for a cluster.

Without a more concrete strategy for resource versioning, it is impossible to have different versions of Auth running
concurrently. Auth needs to be aware of the exact version of a resource that clients are requesting to determine if the
version of said resource stored in the backend is at the same version, if the stored version is capable of being
downgraded to that version, if the stored version is capable of being updated to the requested version, or whether the
request cannot be honored due to version incompatibility.

The only backend operation that uses a locking mechanism is `CompareAndSwap` which means that concurrent writes to the
same resource always result in the last write winning and potentially losing data. This also allows migrations to
unknowingly be reverted if an older version of Auth is able to overwrite an already migrated resource.

We need to ensure that:

1. All backend operations are applied in the order they are received, any outdated requests are rejected.
2. Auth servers know the exact version of a resource requested by clients.
3. Once a migration has been performed, Auth servers that cannot understand the migrated resource version cannot
   overwrite the resource.
4. Migrations are always applied in the correct order and are not skipped.
5. Migrations can be rolled back without having to manually edit the backend.

## Details

### Resource versioning

The version of a resource MUST be bumped when changes are made to it. Any changes to a resource which can be converted
into the previous version and do not cause any backward compatability issues with older Teleport instances only need to
bump the minor version of a resource. All changes to a resource which would cause backward incompatibility with other
Auth servers trying to read/write the resource MUST update the major version. All changes to a resource which alter how
that resource is understood, interpreted, and acted upon by Teleport instances MUST update the major version.

For example, if we originally have a resource like the following at v1:

```proto
syntax = "proto3";

message Foo {
  int32 bar = 1;
}
```

Then adding a new optional field which was handled appropriately, and defaulted to the correct value by application code
if empty only requires bumping the version to v1.1 and not require a direct migration.

```diff
message Foo {
  int32 bar = 1;
+  string baz = 2;
}
```

However, if we were to convert `baz` from a string to `Baz`, that change would not be easily converted into the shape of
`Foo` at v1.1 and would render the new resource unusable by clients that are only aware of `Foo` v1.1. So, the version
of the resource must be bumped from v1.1 to v2 to indicate to clients that a breaking change occurred.

```diff
message Foo {
  int32 bar = 1;
- string baz = 2;
+ reserved 2;
+ reserved baz;
+ Baz baz2 = 3;
}

+ message Baz {
+  string qux = 1;
+  int32 quux = 2;
+ }
```

To date, Auth assumes all client requests are for the version of the resource that Auth is aware of. However, this
causes problems when a resource version is bumped with a breaking change since it is not guaranteed that all Teleport
instances in a cluster are running the same version as Auth. The solution has been to downgrade the resource to the
previous version or alter the resource based on the version of the requester as indicated by the `version` header.

For clients to better communicate which version of a resource they can support there are few options:

1. Add a header that clients must populate with their greatest known version of a resource
2. Version the API such that the version is implied

For major breaking changes a new version of the API is likely warranted, however for smaller changes to a resource it
may be possible to convert the resource into the requested resource version.

This resource versioning scheme will allow Auth servers to determine which resources it knows how to read and write and
which resources it can provide read only access to (possibly with conversion) but it cannot overwrite. Any request which
is honored but causes a conversion to a lower version of a resource MUST alter the resource version of the resource
returned to end with `+downgraded` so that clients can determine how to proceed. The following tables illustrate the
scenarios in which the resource version may vary along a request route and what the outcome of each request will be.

<details open><summary>Rules for writing resources</summary>

| Stored Version                    | Write Version   | Auth Version | Client Version | Force | Outcome                                                         |
| --------------------------------- | --------------- | ------------ | -------------- | ----- | --------------------------------------------------------------- |
| v1.1                              | v1.1            | v1.1         | v1.1           | no    | OK                                                              |
| v1.2                              | v1.1            | v1.1         | v1.1           | no    | ERR (refuse to overwrite new/unknown version)                   |
| v1.2                              | v1.1            | v1.1         | v1.1           | yes   | OK                                                              |
| v1.1                              | v1.2            | v1.2         | v1.2           | \*    | OK                                                              |
| v1.1                              | v1.2            | v1.1         | v1.2           | \*    | ERR (auth never writes a version it doesn't understand)         |
| \*                                | v1.1+downgraded | \*           | \*             | no    | ERR (always refuse to write \*+downgraded)                      |
| \*                                | v1.1+downgraded | v1.1         | \*             | yes   | OK (written as v1.1, metadata stripped)                         |
| \*                                | v1.1+downgraded | v1.0         | \*             | yes   | ERR (awlays refuse to write unknown version, even with --force) |
| /key1/v1.1+downgraded && /key2/v2 | \*              | v1.1         | \*             | no    | ERR (always refuse to write \*+downgraded)                      |
| /key1/v1.1+downgraded && /key2/v2 | \*              | v1.1         | \*             | yes   | OK (written as v1.1, metadata stripped, /key2 is unmodified)    |
| /key1/v1.1+downgraded && /key2/v2 | v1              | v2           | \*             | \*    | OK (written to both /keyv1/v1.1+downgraded, and /key/v2)        |
| /key1/v1.1+downgraded && /key2/v2 | v2              | v2           | \*             | \*    | OK (written to both /keyv1/v1.1+downgraded, and /key/v2)        |

- Stored Version: the version of the resource stored in the backend; multiple keys denotes that the resource was
  recently migrated
- Write Version: the version of the resource to be written into the backend
- Auth Version: the default version of the resource of Auth processing the request
- Client Version: the version of the resource requested by the client
- Force: whether or not the write is forced(e.g tctl create --force)
- Outcome: the result of the operation
</details>

<details open><summary>Rules for reading resources</summary>

| Stored Version                    | Auth Version | Client Version | Outcome                                                |
| --------------------------------- | ------------ | -------------- | ------------------------------------------------------ |
| v1.2                              | v1.1         | v1.1           | OK (version=v1.1+downgraded)                           |
| v1.2                              | v1.1         | v1.2           | OK (version=v1.1+downgraded)                           |
| v2                                | v1.\*        | v1.\*          | ERR (auth cannot auto-downgrade unknown major version) |
| v2                                | v1.\*        | v2             | ERR (auth cannot auto-downgrade unknown major version) |
| v1.1                              | v1.1         | v1             | OK (version=v1+downgraded)                             |
| v1.1                              | v1.1         | v1.2           | OK (version=v1.1)                                      |
| v1.1                              | v1.1         | v2+            | OK (version=v1.1)                                      |
| /key1/v1.1+downgraded && /key2/v2 | v1.1         | v1.1           | OK (version=v1.1+downgraded)                           |
| /key1/v1.1+downgraded && /key2/v2 | v1.1         | v2             | OK (version=v1.1+downgraded)                           |
| /key1/v1.1+downgraded && /key2/v2 | v2           | v2             | OK (version=v2)                                        |

- Stored Version: the version of the resource stored in the backend; multiple keys denotes that the resource was
  recently migrated
- Auth Version: the default version of the resource of Auth processing the request
- Client Version: the version of the resource requested by the client
- Outcome: the result of the operation

</details>

### Optimistic Locking

The backend will be updated to support optimistic locking in order to prevent two simultaneous writes to a resource from
overwriting one another. The resource metadata shall have a new `Revision` field that will include a backend specific
opaque identifier which will be used to reject any writes that do not have a matching `Revision` with the existing item
in the backend. The `Revision` of a resource should not be altered by or counted on to be deterministic by clients, they
should treat the field as an opaque blob and ignore it.

<details open><summary>Metadata changes</summary>

```diff
message Metadata {
  // Name is an object name
  string Name = 1 [(gogoproto.jsontag) = "name"];
  // Namespace is object namespace. The field should be called "namespace"
  // when it returns in Teleport 2.4.
  string Namespace = 2 [(gogoproto.jsontag) = "-"];
  // Description is object description
  string Description = 3 [(gogoproto.jsontag) = "description,omitempty"];
  // Labels is a set of labels
  map<string, string> Labels = 5 [(gogoproto.jsontag) = "labels,omitempty"];
  // Expires is a global expiry time header can be set on any resource in the
  // system.
  google.protobuf.Timestamp Expires = 6 [
    (gogoproto.stdtime) = true,
    (gogoproto.nullable) = true,
    (gogoproto.jsontag) = "expires,omitempty"
  ];
  // ID is a record ID
  int64 ID = 7 [(gogoproto.jsontag) = "id,omitempty"];
+  Revision is an opaque identifier used to enforce optimistic locking.
+  string Revision = 8 [(gogoproto.jsontag) = "revision"];
}
```

</details>

In other words, when a resource is written the `Revision` is altered by the backend. However, prior to the resource
being written the `Revision` of the new value and the existing resource in the backend are compared, if they match then
the update is permitted, if they differ then the update is rejected. So, if two clients try to update the same value
concurrently, only the first write will succeed and the second will be rejected. The second client will have to fetch
the resource, apply their change and try to update again.

The backend interface will be extended to support new conditional delete and update methods which enforce optimistic
locking. Most if not all user facing and editable resources should use the new optimistic locking primitives to prevent
losing changes made by a user. Resources which are updated based on presence are likely not a good candidate for
conditional operations due to the amount of stress that may put on backends.

<details open><summary>Backend changes</summary>

```diff
type Backend interface {
  ...
+ ConditionalPut(ctx context.Context, i Item) (*Lease, error)
+ ConditionalUpdate(ctx context.Context, i Item) (*Lease, error)
+ ConditionalDelete(ctx context.Context, key []byte, rev string) (*Lease, error)
}
```

</details>

### When is a migration required?

Not every backend resource change must be accompanied by a migration. Small additive changes, or converting one field to
another may handle the migration lazily on read and write. Larger scale changes that alter the shape of the resource
like converting a resource to one or many other resources, converting a field to a repeated value, or changing how the
resource is encoded in the backend should include a direct migration.

### Persistent Migrations

All new direct migrations MUST exist in perpetuity to allow for the correct migrations to be applied regardless of which
version of Teleport is being upgraded from and to. Migrations MUST be numbered and applied in sequence. The order of
migrations MUST not change and a migration MUST not be altered once it has been included in a release.

To make tracking and discovering migrations easier all migrations MUST be placed in `lib/auth/migrations`. Each
migration should be named `$number-migration-description.go` and contain a single migration. The following is an example
of four migrations:

```
./lib/auth/migrations/
├── 0001-initial_migration.go
├── 0002-another_migration.go
├── 0003-some_other_migration.go
└── 0004-yet_another_migration.go
```

Migration history is to be stored in the backend to enable tracking which migrations have been applied, when, and
whether they were successful or not. The key `/migrations/<migration_number>` will store the history of a migration.

Cluster admins may view the migration history via `tctl migrations ls`.

### Migration Strategy

All migrations associated with a breaking change MUST not be performed in place, instead these migrations MUST migrate
both the keys and values in the backend. For example, to migrate the data stored in key `some/path/to/resource/<ID>` we
must leave the original value at `some/path/to/resource/<ID>` as is and write the migrated value to
`some/new/path/to/resource/<ID>`. This allows older versions of Teleport to still operate on
`some/path/to/resource/<ID>`, while newer versions can first attempt reads from `some/new/path/to/resource/<ID>`, and
fall back to reading from `some/path/to/resource/<ID>` if the migrated key does not exist yet.

Moving data to a new key range and deleting the data from the original key range in the same migration will prevent
downgrades unless an explicit step is taken to undo the migration. To avoid this scenario a migration may be split into
two, one that migrates the data to a new key range in and one that deletes the old key range; however the two migrations
should not exist in the same release. Continuing from the example above, if `some/path/to/resource/<ID>` is migrated to
`some/new/path/to/resource/<ID>` in v1.0.0 a follow-up migration should be added in v2.0.0 to delete the keys under
`some/path/to/resource` that were migrated in v1.0.0.

Keys should be versioned to determine which version of a resource exists at any given key range. A key prefix of
`/type/version/subkind/name` should be used where possible to have uniformity. For example if nodes were migrated from
`/nodes/default/<UUID>` it should be to `/nodes/v2/default/<UUID>`.

When a migration converts a resource at the new key, the corresponding resource at the old key must also have its
version appended with `+downgraded`. For example converting a resource at v1.1 to v2 via a migration should update the
old resource to now have a version of `v1.1+downgraded`. This is an indication to older Auth servers that the resource
is now read only.

### Implementation Details

#### Auth Initialization

Every time Auth starts it will evaluate all known migrations against the cluster migration history and apply any
outstanding migrations. This will allow clusters to skip major versions when upgrading and still have the correct
migrations applied in order. However, due to the fact that migration history is not persisted today, upgrades must be
done in sequence up until the very first release that contains the changes outlined in this RFD.

All migrations will be performed in a background goroutine that is spawned from `auth.Init` to prevent them from
blocking Auth initialization. For each migration that needs to be applied Auth will attempt to acquire the lock
`/migrations/lock/<migration_number>` for the duration of that migration execution to prevent simultaneous instances
from running the same migration. When Auth acquires the lock it must check the status of the migration to determine if
that migration was already completed by another instance, if it was then no action is to be taken. If another instance
of Auth attempted the migration but failed, then the next instance that gets the lock should attempt the migration.
After successfully applying a migration Auth will store that migrations status in the backend and then move on to the
next migration until none remain, repeating the same process for each migration.

In the event that all Auth instances fail to complete a single migration, no further migrations may be applied, and the
migration routine MUST exit. Logs should be emitted that indicate why the migration failed, the `teleport_migrations`
metric should indicate a failure to allow admins to enhance observability. Cluster admins can also interrogate the
migrations via `tctl migrations ls` to see any errors associated with a particular migration. After the issue is
resolved through manual intervention `tctl migrations apply` can be invoked to kick off the migration process mentioned
above.

#### Migrations

A new framework will be created to declare migrations, perform migrations in the correct order, and persist migration
status. A migration must implement the following interface:

```go
type Migration interface {
    // Up applies a migration on the provided [backend.Backend].
    Up(ctx context.Context, b backend.Backend) error
    // Down undoes a migration on the provided [backend.Backend].
    Down(ctx context.Context, b backend.Backend) error
}
```

A migration that converts nodes to be stored in binary proto instead of json might look like the following:

<details open><summary>Example migration</summary>

```go
// exampleMigration converts the encoding used to
// store [types.Server] from json to proto.
type exampleMigraion struct {}

// Up converts any existing [types.Server]
func (e exampleMigration) Up(ctx context.Context, b backend.Backend) error {
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

func (e exampleMigration) Down(ctx context.Context, b backend.Backend) error {
    oldSvc, err := generic.NewService(&generic.ServiceConfig[types.Server]{
    Backend:       b,
	ResourceKind:  types.KindNode,BackendPrefix: backend.Key(nodesPrefix, namespace),
    MarshalFunc:   services.MarshalServer,
    UnmarshalFunc: services.UnmarshalServer,
    })
    if err != nil {
        return trace.Wrap(err)
    }

    newSvc, err := generic.NewService(&generic.ServiceConfig[types.Server]{
    Backend:       b,
    ResourceKind:  types.KindNode,
    BackendPrefix: backend.Key(nodesPrefixV2, namespace),;''
    MarshalFunc:   func(t types.Server) ([]byte, error) {
        return proto.Marshal(t)
    }
    UnmarshalFunc: func(b []byte) (types.Server, error) {
        var s types.ServerV2
        if err :=  proto.Unmarshal(b, s); err != nil {
            return nil, err,
        }
        return s, nil
    })
    if err != nil {
        return trace.Wrap(err)
    }

    servers, err := newSvc.GetResources(ctx)
    if err != nil {
        if trace.IsNotFound(err) {
            return nil
        }
        return trace.Wrap(err)
    }

    for _, s := range servers {
        if err := oldSvc.CreateResource(ctx, s); err != nil {
            return trace.Wrap(err)
        }
	}

    return nil
}
```

</details>

Migration registration requires the version of the migration and the implementation, if a migration with the same
version is already exists then registration will fail.

```go
    if err := migrate.Register(1, exampleMigration{}); err != nil {
		return err
    }
```

#### Backend Services

The generic backend service will be extended to handle support for optimistic locking, performing version checking, and
downgrading resources. This will reduce the burden on each custom backend service and ease the developer experience. To
opt in to the new behavior a resources backend service just needs to ensure that it is using the
`lib/services/generic.Service`.

### Backward Compatibility

Migrations have historically been backward incompatible operations. Migrations altered the data in place without
changing the key or resource version, which can prevent any versions prior to the migration from being able to unmarshal
the value into the correct representation. The only way to downgrade in this scenario was to restore the backend from a
backup prior to the migration, attempt to manually roll back the migration, or deleting the entire key range that was
migrated.

To rollback a migration we can either add a `tctl migrations down <number_of_versions>` that will perform the `Down`
migration on the last provided number of versions. If the current migration value is 3 and a user ran
`tctl migrations down 2` then both migration 3 and 2 would be rolled back and leave migration 1 as the current version.
This is potentially dangerous though because Auth may be running and expecting that certain migrations exits. Another
option would be to add `teleport migrations down <number_of_versions>` which functioned the same as the `tctl` variation
except that would be meant to run when Auth is not running. It would start Teleport, perform the migrations, and then
exit. So if a user upgraded their cluster and something broke during the automatic migrations applied in that version
they could stop Auth, run `teleport migrations down`, and then start Auth at the previous Teleport version.

### Testing Migrations

While the framework laid out in this RFD allows migrations to be applied in a deterministic manner, it does not provide
a uniform rule or process for any code that is impacted by a migration. To ensure that a migration is functional testing
should consider a wide range of simultaneous versions in a cluster in accordance to our version compatibility matrix.
Imagine that we are going to introduce a migration in v3.0.0, we must test the following for an extended period of
time(10m) to ensure all supported versions are functional:

| Auth 1 | Auth 2  | Proxy   | Agents  |
| ------ | ------- | ------- | ------- |
| v3.0.0 | v3.0.0  | v3.0.0  | v3.0.0  |
| v3.0.0 | <v3.0.0 | <v3.0.0 | <v3.0.0 |
| v3.0.0 | v3.0.0  | <v3.0.0 | <v3.0.0 |
| v3.0.0 | v3.0.0  | v3.0.0  | <v3.0.0 |

Testing multiple versions of Auth at the same time will help validate that the migration is backward compatible and that
a rollback is possible. Ensuring that Auth running with the migration and all other instances without the migration is
also crucial to test since Auth is always the first component updated. If the migration is unknown by the agents it
should not impact their ability to operate.

It can also be a worthwhile exercise to run through the same testing matrix above for any backend changes that require a
direct migration. Even adding a new field to an existing resource can have
[drastic consequences](https://github.com/gravitational/teleport/issues/25644) if the previous version cannot unmarshal
the unknown field. A mixed fleet with agents running an older version of Teleport than Auth can also result in undefined
behavior new fields in a resource have an impact on business logic.

### Security

Migrations already exist today, this RFD only proposes a way to make them deterministic and elevates visibility into
migration history. Only users with the correct permissions will be able to invoke `tctl migrations apply` and in most
cases the command should result in a no-op. We will also only allow a single migration process to be in flight at any
given time.

### UX

Cluster admins will have a much simpler and straightforward upgrade procedure to follow which should help reduce some of
the support load. Performing migrations in a way that eliminates the need to change the number of Auth replicas should
also result in a more stable cluster during and after upgrades.

This should have the biggest impact on Cloud tenants that experience some outages during upgrades. By allowing multiple
instances of Auth to exist during an upgrade event we will be able to reduce downtime experienced by users.

`tctl migrations ls` and `tctl migrations apply` will be added to allow admins to inspect the status of the migrations
and to retry applying migrations in the event that one fails.

### Proto Specification

<details open><summary>MigrationService</summary>

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

// Request for ApplyMigrations.
message ApplyMigrationsRequest {}

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
  google.protobuf.Timestamp started_at = 6;
  // The timestamp that the migration finished executing.
  google.protobuf.Timestamp completed_at = 7;
  // Whether the outcome of the migration was successful.
  bool success = 8;
  // A friendly message that describes the output of the migration.
  // If the migration failed it will contain the error, if the migration
  // was applied cleanly it will contain "success".
  string message = 9;
}
```

</details>

### Audit Events

Audit events will be emitted when a migration is started and when it is completed:

- `TMI001I` / MigrationStart
- `TMI002I` / MigrationComplete
- `TMI002W` / MigrationFailure

<details open><summary>Event definition</summary>

```proto
message MigrationEvent {
  Metadata metadata = 1;
  Status status = 2;
  int migration = 3;
  bool manual = 4;
}
```

### Observability

The existing `teleport_migrations` metric will be reused to record when a migration has been performed. Tracing will
also be added with a root span created by the migration framework and a child span per migration performed.
