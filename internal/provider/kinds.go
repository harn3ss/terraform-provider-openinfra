package provider

// The open-infra CRDs, described once each.
//
// KEEP IN SYNC with platform/abstraction/*-xrd.yaml in the open-infra repo. The XRD is
// the source of truth; this table is a hand-maintained mirror, and a field missing here
// simply cannot be expressed in HCL — no error, just an absence.
//
// Conventions, and why:
//
//   - Default mirrors the XRD default and makes the attribute Optional+Computed, so the
//     server filling it in is not reported as drift. Nested attributes deliberately
//     carry NO Default — a default inside a nested block forces the whole block to be
//     computed, which produces worse plan output than documenting the value. Their
//     defaults are stated in the description instead.
//   - Replaces marks fields the platform cannot change in place. When in doubt, prefer
//     replace: silently accepting an update the Composition ignores is the worse
//     failure, because the config then lies about what is running.
//   - Enum values are listed in the description rather than validated here, so that
//     adding a value to an XRD doesn't require a provider release to become usable.

// connSpec is the shared source/target/site block used by Migration, Replication,
// DataFlow nodes and Stream. Declared once because these are the same object in the
// XRDs (Replication's siteB is literally a YAML alias of siteA).
func connSpec(extra ...attr) []attr {
	base := []attr{
		{Name: "engine", Type: tString, Required: true,
			Description: "One of `postgres`, `mysql`, `mariadb`, `sqlserver`."},
		{Name: "host", Type: tString, Required: true, Description: "Hostname or Service name."},
		{Name: "port", Type: tInt, Description: "Defaults to `5432`."},
		{Name: "database", Type: tString, Required: true},
		{Name: "username", Type: tString, Required: true},
		{Name: "password_secret_ref", Type: tObject, Required: true,
			Description: "Secret holding the password. The password itself is never in this resource.",
			Nested: []attr{
				{Name: "name", Type: tString, Required: true},
				{Name: "key", Type: tString, Description: "Defaults to `password`."},
			}},
		{Name: "ssl", Type: tBool, Description: "Defaults to `false`."},
	}
	return append(base, extra...)
}

// ruleEndpoints is the from/to block of a SecurityGroup rule.
func ruleEndpoints(name, desc string) attr {
	return attr{Name: name, Type: tObjectList, Description: desc, Nested: []attr{
		{Name: "cidr", Type: tString, Description: "An IP range, e.g. `10.0.0.0/8`."},
		{Name: "security_group", Type: tString, Description: "Another SecurityGroup, by name."},
		{Name: "namespace", Type: tString, Description: "Every pod in a namespace."},
	}}
}

func securityRule(direction, endpointName, endpointDesc string) attr {
	return attr{Name: direction, Type: tObjectList, Nested: []attr{
		{Name: "protocol", Type: tString, Description: "`TCP` or `UDP`. Defaults to `TCP`."},
		{Name: "description", Type: tString, Description: "Why this rule exists. Carried through to the NetworkPolicy."},
		{Name: "ports", Type: tIntList, Description: "Ports to allow. Empty means all ports."},
		ruleEndpoints(endpointName, endpointDesc),
	}}
}

// genericKinds is every kind served by the table-driven resource. Application,
// Database and VirtualMachine are NOT here: they have bespoke behaviour (per-engine
// connection-secret naming, start/stop semantics) and keep their hand-written
// implementations.
var genericKinds = []kindSpec{
	{
		TypeName: "function", Kind: "Function", Plural: "functions",
		Description: "A serverless function — a container that scales to zero between requests. " +
			"Compiles to a Knative Service.",
		Attrs: []attr{
			{Name: "image", Type: tString, Required: true,
				Description: "Container image serving HTTP."},
			{Name: "port", Type: tInt, Default: int64(8080), Description: "Port the container listens on."},
			{Name: "gpu", Type: tInt, Default: int64(0),
				Description: "GPUs per instance, for serverless inference. `0` is CPU-only."},
			{Name: "memory", Type: tString,
				Description: "Guaranteed memory per instance as a Kubernetes quantity (e.g. `512Mi`, `1Gi`) — the AWS Lambda memory-size knob. Set as both request and limit; unset means the cluster default."},
			{Name: "timeout", Type: tInt,
				Description: "Max wall-clock seconds per request (Knative revision timeout) — the AWS Lambda timeout knob. Unset means the Knative default (300s)."},
			{Name: "expose", Type: tBool, Default: true,
				Description: "Reachable on the LAN via the Knative gateway. `false` keeps it cluster-local."},
			{Name: "scaling", Type: tObject, Nested: []attr{
				{Name: "min", Type: tInt, Description: "Minimum instances. Defaults to `0` — scale to zero."},
				{Name: "max", Type: tInt, Description: "Defaults to `10`."},
				{Name: "target", Type: tInt, Description: "Target concurrent requests per pod. Defaults to `100`."},
			}},
			{Name: "queues", Type: tStringList,
				Description: "Queues this function works with. Injects `NATS_URL` and `OPENINFRA_QUEUES`."},
			{Name: "env", Type: tObjectList, Nested: []attr{
				{Name: "name", Type: tString, Required: true},
				{Name: "value", Type: tString},
			}},
			{Name: "secrets", Type: tStringList,
				Description: "Secrets to inject with `envFrom` — an app's database or bucket credentials, say."},
			{Name: "security_groups", Type: tStringList,
				Description: "SecurityGroups to attach to this function's pods."},
			{Name: "trigger", Type: tObject,
				Description: "Event-source mapping: deliver a Stream's change events to this function as HTTP POSTs.",
				Nested: []attr{
					{Name: "stream", Type: tString, Description: "Stream name to consume."},
					{Name: "subject", Type: tString, Description: "Subject filter. Defaults to `cdc.<stream>.>`."},
				}},
		},
		Status: []statusAttr{{Name: "url", Type: tString, Description: "The function's HTTP endpoint."}},
	},

	{
		TypeName: "volume", Kind: "Volume", Plural: "volumes",
		Description: "A block volume you can attach to a virtual machine — the EBS-shaped primitive. " +
			"Backed by Longhorn.",
		Attrs: []attr{
			{Name: "size", Type: tString, Required: true, Description: "e.g. `50Gi`. Expandable later."},
			{Name: "migratable", Type: tBool, Default: false, Replaces: true,
				Description: "RWX block on the `longhorn-migratable` class, so the volume can attach to a " +
					"live-migratable (`high_availability`) VM without blocking migration. `false` is RWO. " +
					"The access mode is fixed at creation, so changing this replaces the volume."},
			{Name: "source", Type: tObject, Replaces: true,
				Description: "Restore a new volume from a snapshot. Only meaningful at creation.",
				Nested: []attr{
					{Name: "snapshot", Type: tString, Description: "VolumeSnapshot name in the same namespace."},
				}},
		},
		Status: []statusAttr{
			{Name: "phase", Type: tString},
			{Name: "actual_size", Path: []string{"size"}, Type: tString,
				Description: "The size actually provisioned, which can lag `size` during an expansion."},
		},
	},

	{
		TypeName: "file_share", Kind: "FileShare", Plural: "fileshares",
		Description: "A shared filesystem exported over SMB — the EFS/FSx-shaped primitive.",
		Attrs: []attr{
			{Name: "size", Type: tString, Required: true, Description: "Share capacity, e.g. `100Gi`."},
			{Name: "expose", Type: tBool, Default: true,
				Description: "Get a LAN IP on SMB 445 so machines can mount it."},
			{Name: "node_ip", Path: []string{"nodeIP"}, Type: tString,
				Description: "Also answer SMB 445 on this node's LAN IP, for masquerade-networked VMs that " +
					"cannot reach the MetalLB address."},
		},
		Status: []statusAttr{{Name: "share", Type: tString, Description: "The UNC path to mount."}},
	},

	{
		TypeName: "security_group", Kind: "SecurityGroup", Plural: "securitygroups",
		Description: "A named set of firewall rules attachable to workloads and VMs. " +
			"Compiles to NetworkPolicies; members are default-deny inbound once attached.",
		Attrs: []attr{
			securityRule("ingress", "from", "Sources, OR'd together. Each entry is one of `cidr`, `security_group` or `namespace`."),
			securityRule("egress", "to", "Destinations, OR'd together."),
		},
		Status: []statusAttr{
			{Name: "member_label", Type: tString, Description: "Pods carrying this label are members."},
		},
	},

	{
		TypeName: "model", Kind: "Model", Plural: "models",
		Description: "A served language model with an OpenAI-compatible endpoint.",
		Attrs: []attr{
			{Name: "model", Type: tString, Required: true, Replaces: true,
				Description: "From the curated catalog: `qwen2.5:0.5b`, `llama3.2:1b`, `llama3.2:3b`, " +
					"`llama3.1:8b`, `mixtral:8x7b`."},
			{Name: "high_availability", Type: tBool, Default: false,
				Description: "Two replicas on separate nodes."},
			{Name: "domain", Type: tString, Description: "Hostname for an external Ingress and TLS certificate."},
			{Name: "storage_size", Type: tString, Default: "20Gi",
				Description: "Weight-cache size limit."},
			{Name: "expose", Type: tBool, Default: false, Description: "Get a LAN IP via MetalLB."},
		},
		Status: []statusAttr{{Name: "endpoint", Type: tString, Description: "The OpenAI-compatible base URL."}},
	},

	{
		TypeName: "query", Kind: "Query", Plural: "queries",
		Description: "A one-shot SQL query over object storage — the Athena-shaped primitive. " +
			"Results are written to a bucket as CSV.\n\n" +
			"Note this is a **job, not a desired state**: changing `sql` runs a new query rather than " +
			"altering the old one, so the field forces replacement.",
		Attrs: []attr{
			{Name: "sql", Type: tString, Required: true, Replaces: true,
				Description: "The SQL to run. duckdb reads with `read_parquet('s3://…')`; trino uses catalog tables."},
			{Name: "engine", Type: tString, Default: "duckdb", Replaces: true,
				Description: "`duckdb` for serverless single-node schema-on-read, or `trino` for the " +
					"federating warehouse engine."},
			{Name: "output_bucket", Type: tString, Default: "query-results", Replaces: true,
				Description: "MinIO bucket for the CSV result and its `<id>.metadata.json`. Created if absent."},
		},
		Status: []statusAttr{
			{Name: "query_id", Type: tString},
			{Name: "result_location", Type: tString, Description: "`s3://` URI of the result object."},
			{Name: "phase", Type: tString},
		},
	},

	{
		TypeName: "migration", Kind: "Migration", Plural: "migrations",
		Description: "A one-way database migration — the DMS-shaped primitive. Loads a source database " +
			"into a target and optionally keeps it current with change data capture.",
		Attrs: []attr{
			{Name: "mode", Type: tString, Default: "full-load-and-cdc",
				Description: "`full-load`, `cdc`, or `full-load-and-cdc`."},
			{Name: "source", Type: tObject, Required: true, Nested: connSpec(
				attr{Name: "schemas", Type: tStringList, Description: "Defaults to `[\"public\"]`."},
			)},
			{Name: "target", Type: tObject, Required: true, Nested: connSpec(
				attr{Name: "schema", Type: tString, Description: "Optional target schema override."},
			)},
			{Name: "tables", Type: tStringList,
				Description: "Bare table names to move. Empty means all tables."},
		},
		Status: []statusAttr{
			{Name: "phase", Type: tString},
			{Name: "stream", Type: tString, Description: "The JetStream stream carrying this migration's change events."},
		},
	},

	{
		TypeName: "replication", Kind: "Replication", Plural: "replications",
		Description: "Bidirectional replication between two databases (multi-master, last-write-wins).\n\n" +
			"This is **experimental**: convergence under concurrent conflicting writes is not yet " +
			"proven by a formal harness. See the open-infra README's maturity section.",
		Attrs: []attr{
			{Name: "site_a", Type: tObject, Required: true, Nested: connSpec(
				attr{Name: "name", Type: tString, Required: true,
					Description: "Short site id used as the origin marker, e.g. `a` or `east`. Must be unique across the pair."},
				attr{Name: "schema", Type: tString, Description: "Defaults to `public`."},
			)},
			{Name: "site_b", Type: tObject, Required: true, Nested: connSpec(
				attr{Name: "name", Type: tString, Required: true, Description: "See `site_a.name`."},
				attr{Name: "schema", Type: tString, Description: "Defaults to `public`."},
			)},
			{Name: "tables", Type: tStringList, Required: true,
				Description: "Bare table names replicated both ways. Each must exist on both sites with the same primary key."},
			{Name: "version_column", Type: tString, Default: "_mm_version",
				Description: "Hybrid-logical-clock column added by table preparation, used for last-write-wins."},
			{Name: "origin_column", Type: tString, Default: "_mm_origin",
				Description: "Origin marker used for loop prevention and as the last-write-wins tiebreak."},
		},
		Status: []statusAttr{{Name: "phase", Type: tString}},
	},

	{
		TypeName: "dataflow", Kind: "DataFlow", Plural: "dataflows",
		Description: "A whole data topology in one resource: database, topic, function and bucket nodes " +
			"joined by replication, migration, stream and pipe edges. This is what the console's " +
			"canvas edits.",
		Attrs: []attr{
			{Name: "nodes", Type: tObjectList, Required: true,
				Description: "Endpoints on the canvas. `name` is the node id edges refer to, and doubles as " +
					"the replication origin marker.",
				Nested: append([]attr{
					{Name: "name", Type: tString, Required: true},
					{Name: "role", Type: tString,
						Description: "`database`, `topic`, `function` or `bucket`. Defaults to `database`."},
					{Name: "function_ref", Type: tString, Description: "For `role = function`: the Function's name."},
					{Name: "function_url", Type: tString, Description: "For `role = function`: an explicit URL instead."},
					{Name: "bucket", Type: tString, Description: "For `role = bucket`."},
					{Name: "prefix", Type: tString, Description: "For `role = bucket`: key prefix."},
					{Name: "x", Type: tInt, Description: "Canvas position. Cosmetic; the console maintains it."},
					{Name: "y", Type: tInt, Description: "Canvas position. Cosmetic; the console maintains it."},
					{Name: "schema", Type: tString, Description: "Defaults to `public`."},
				}, connDBFields()...)},
			{Name: "edges", Type: tObjectList, Required: true,
				Description: "Data movement between nodes.",
				Nested: []attr{
					{Name: "from", Type: tString, Required: true, Description: "Source node name."},
					{Name: "to", Type: tString, Required: true,
						Description: "Target node name. For replication edges the link is bidirectional."},
					{Name: "type", Type: tString, Required: true,
						Description: "`replication`, `migration`, `stream` or `pipe`."},
					{Name: "mode", Type: tString,
						Description: "Migration edges only: `full-load`, `cdc` or `full-load-and-cdc`. " +
							"Defaults to `full-load-and-cdc`."},
					{Name: "bootstrap", Type: tBool,
						Description: "Replication edges: create the target's tables from `from` before syncing. " +
							"Defaults to `false`."},
				}},
			{Name: "tables", Type: tStringList,
				Description: "Tables moved on every edge of this flow, e.g. `[\"public.orders\"]`, or `[\"*\"]` " +
					"for all. Scope is per-flow; per-edge subsets are not supported."},
			{Name: "version_column", Type: tString, Default: "_mm_version"},
			{Name: "origin_column", Type: tString, Default: "_mm_origin"},
			{Name: "auto_sync_tables", Type: tBool, Default: false,
				Description: "Multi-master only: keep the table *set* in sync across all members, adding a new " +
					"table everywhere when it appears anywhere. Implies capturing all tables."},
		},
		Status: []statusAttr{{Name: "phase", Type: tString}},
	},

	{
		TypeName: "stream", Kind: "Stream", Plural: "streams",
		Description: "Change data capture from a database into NATS JetStream — the Kinesis-shaped " +
			"primitive. Consume it from a `openinfra_function` with a `trigger`.",
		Attrs: []attr{
			{Name: "source", Type: tObject, Required: true, Nested: connSpec(
				attr{Name: "schemas", Type: tStringList, Description: "Defaults to `[\"public\"]`."},
				attr{Name: "tables", Type: tStringList,
					Description: "Tables or collections to capture, bare names. Empty means all."},
				attr{Name: "replica_set", Type: tString,
					Description: "MongoDB only. Defaults to `rs0`."},
			)},
		},
		Status: []statusAttr{
			{Name: "stream", Type: tString, Description: "The JetStream stream backing this resource."},
			{Name: "subjects", Type: tString, Description: "The subject wildcard change events publish to."},
			{Name: "phase", Type: tString},
		},
	},

	{
		TypeName: "directory", Kind: "Directory", Plural: "directories",
		Description: "A Samba Active Directory domain controller — the AWS Managed Microsoft AD-shaped " +
			"primitive. Machines join it manually; see the open-infra docs.",
		Attrs: []attr{
			{Name: "domain", Type: tString, Required: true, Replaces: true,
				Description: "Domain FQDN, lowercase, e.g. `corp.example.lan`. The Kerberos realm and NetBIOS " +
					"name are derived from it, so it cannot be changed in place."},
			{Name: "size", Type: tString, Default: "5Gi", Description: "Storage for the directory database."},
			{Name: "expose", Type: tBool, Default: true,
				Description: "Get a LAN IP for DNS, Kerberos, LDAP and SMB so machines can join."},
		},
	},

	{
		TypeName: "fault_injection", Kind: "FaultInjection", Plural: "faultinjections",
		Description: "A deliberate fault, for resilience testing — the FIS-shaped primitive. " +
			"Compiles to a Chaos Mesh experiment.\n\n" +
			"Applying this **breaks things on purpose**. Scope `target` carefully: the blast radius is " +
			"whatever `label_selector` matches.",
		Attrs: []attr{
			{Name: "type", Type: tString, Required: true, Replaces: true,
				Description: "`pod-kill`, `pod-failure`, `network-latency`, `network-loss`, `network-partition`, " +
					"`stress-cpu`, `stress-memory`, `clock-skew`, `io-latency`."},
			{Name: "target", Type: tObject, Required: true,
				Description: "The blast radius.",
				Nested: []attr{
					{Name: "namespace", Type: tString, Description: "Defaults to the FaultInjection's own namespace."},
					{Name: "label_selector", Type: tStringMap, Required: true,
						Description: "Pod labels to target, e.g. `{ app = \"pg\" }`."},
				}},
			{Name: "mode", Type: tString, Default: "one",
				Description: "How many matched pods to hit: `one`, `all` or `fixed-percent`."},
			{Name: "value", Type: tString, Description: "For `mode = fixed-percent`, e.g. `\"50\"`."},
			{Name: "duration", Type: tString, Default: "60s",
				Description: "How long the fault lasts. Ignored by `pod-kill`."},
			{Name: "latency", Type: tString, Default: "200ms", Description: "Delay for `network-latency` / `io-latency`."},
			{Name: "loss", Type: tString, Default: "50", Description: "Packet-loss percent for `network-loss`."},
			{Name: "direction", Type: tString, Default: "to", Description: "`to`, `from` or `both`."},
			{Name: "partition_peer", Type: tStringMap,
				Description: "`network-partition` only: pod labels the target is cut off from. " +
					"Omit to isolate it from everything."},
			{Name: "cpu_workers", Type: tInt, Default: int64(1)},
			{Name: "cpu_load", Type: tInt, Default: int64(80), Description: "Load percent per worker."},
			{Name: "memory", Type: tString, Default: "256MB", Description: "Size to consume for `stress-memory`."},
			{Name: "time_offset", Type: tString, Default: "+5m", Description: "Offset for `clock-skew`, e.g. `+5m` or `-1h`."},
			{Name: "volume_path", Type: tString, Default: "/data", Description: "Mount path to slow for `io-latency`."},
		},
		Status: []statusAttr{{Name: "phase", Type: tString}},
	},

	{
		TypeName: "vm_image", Kind: "VmImage", Plural: "vmimages",
		Description: "A golden Windows disk image built from an evaluation ISO, for `openinfra_virtual_machine` " +
			"to clone. Building one takes a long time and is not idempotent, so every field replaces.",
		Attrs: []attr{
			{Name: "os", Type: tString, Required: true, Replaces: true,
				Description: "`windows-server-2019`, `windows-server-2022` or `windows-server-2025`."},
			{Name: "source_url", Type: tString, Replaces: true, Description: "Override the evaluation ISO URL."},
			{Name: "disk_size", Type: tString, Default: "64Gi", Replaces: true, Description: "Golden disk size."},
			{Name: "existing_golden_claim", Type: tString, Replaces: true,
				Description: "Adopt an existing PVC as the golden image, skipping the ISO build entirely."},
		},
		Status: []statusAttr{{Name: "phase", Type: tString}},
	},
	{
		TypeName: "http_api", Kind: "HttpApi", Plural: "httpapis",
		Description: "An API-Gateway-style HTTP front door: a hostname with path routes onto " +
			"`openinfra_function` or `openinfra_application` backends. Compiles to a Traefik Ingress " +
			"with cert-manager TLS.",
		Attrs: []attr{
			{Name: "domain", Type: tString, Required: true,
				Description: "Hostname the API is served on, e.g. `api.example.com`."},
			{Name: "tls", Type: tBool, Default: true,
				Description: "Terminate TLS via cert-manager. Set false for plain HTTP."},
			{Name: "routes", Type: tObjectList, Required: true, Nested: []attr{
				{Name: "path", Type: tString, Required: true, Description: "URL path to match, e.g. `/` or `/users`."},
				{Name: "path_type", Type: tString, Description: "`Prefix` (default), `Exact`, or `ImplementationSpecific`."},
				{Name: "backend", Type: tObject, Nested: []attr{
					{Name: "kind", Type: tString, Description: "`Function` (default) or `Application`."},
					{Name: "name", Type: tString, Required: true, Description: "Name of the backing Function/Application (same namespace)."},
					{Name: "port", Type: tInt, Description: "Backend Service port. Defaults to `80`."},
				}},
			}},
		},
		Status: []statusAttr{{Name: "url", Type: tString, Description: "The public URL of the API."}},
	},
	{
		TypeName: "graphql_api", Kind: "GraphQLApi", Plural: "graphqlapis",
		Description: "A neutral GraphQL API served by the open-appsync engine: a schema plus data " +
			"sources and resolvers, each resolver declaring a `runtime` (today `appsync-vtl`) and " +
			"carrying its request/response mapping templates inline. A team's existing AppSync VTL runs " +
			"byte-for-byte unchanged. Compiles to an engine Deployment + Service (EXPERIMENTAL).",
		Attrs: []attr{
			{Name: "schema", Type: tString,
				Description: "GraphQL SDL for the API. When set, the engine parses it into an in-memory type graph " +
					"and answers `__schema`/`__type` introspection over it, so GraphQL tooling can build a client " +
					"schema. Optional: the executor still resolves fields from the resolver list without it, but " +
					"introspection is then unavailable."},
			{Name: "image", Type: tString, Default: "ghcr.io/harn3ss/open-infra-open-appsync:latest",
				Description: "The open-appsync engine image serving this API."},
			{Name: "replicas", Type: tInt, Default: int64(1)},
			{Name: "mongo_uri", Type: tString, Path: []string{"mongoURI"},
				Description: "FerretDB (Mongo wire) endpoint backing `dynamodb` data sources. Required only " +
					"if a data source is `dynamodb`; a `dynamodb` source with no `mongo_uri` fails closed at load."},
			{Name: "api_keys_secret", Type: tString,
				Description: "Name of a Secret (in this API's namespace) holding `apikeys.json` — a JSON array of " +
					"`{key, username, groups}`. Enables `@aws_api_key` auth. THE KEY IS AN IDENTITY: presenting a key " +
					"authenticates the request AS the mapped k8s identity (username/groups), whose SubjectAccessReview " +
					"then authorizes `@aws_api_key` fields — the same one policy world as every other front door. Keep " +
					"key material in this Secret, never in the spec. `@aws_iam`/`@aws_oidc`/`@aws_cognito_user_pools` " +
					"are likewise enforced when their directives appear; `@aws_lambda` is advisory (reported, not enforced)."},
			{Name: "mongo_db", Type: tString, Path: []string{"mongoDB"}, Default: "open_appsync",
				Description: "FerretDB database name for `dynamodb` data sources."},
			{Name: "limits", Type: tObject, Description: "Hostile-load guards; ON by default even if omitted.", Nested: []attr{
				{Name: "max_depth", Type: tInt, Description: "Reject queries nested past this (default 10; negative disables)."},
				{Name: "max_cost", Type: tInt, Description: "Reject queries with more fields than this (default 1000; negative disables)."},
				{Name: "persisted_only", Type: tBool, Description: "Only pre-registered documents in `persisted_queries` run."},
				{Name: "persisted_queries", Type: tStringList, Description: "Allow-listed query documents (for `persisted_only`)."},
				{Name: "introspection", Type: tString,
					Description: "Who may read the schema via `__schema`/`__type`: `enabled` (default, any client — " +
						"best for tooling), `disabled` (never), or `authenticated-only` (off for anonymous callers). Requires `schema`."},
			}},
			{Name: "data_sources", Type: tObjectList, Nested: []attr{
				{Name: "name", Type: tString, Required: true, Description: "Data source name a resolver references."},
				{Name: "type", Type: tString, Description: "`memory` (default, ephemeral), `none` (no backend — " +
					"resolved in the mapping templates, for pub/sub-only fields and local computation), `dynamodb` " +
					"(FerretDB-backed), `http` (an HTTP endpoint), `lambda` (invoke a `kind: Function` over HTTP), " +
					"`rds` (SQL over PostgreSQL/Aurora-PostgreSQL, DSN in `connection_secret`), `opensearch` (search " +
					"against an OpenSearch domain), or `eventbridge` (publish events to the NATS event bus)."},
				{Name: "collection", Type: tString, Description: "`dynamodb`: the FerretDB collection (the 'table')."},
				{Name: "endpoint", Type: tString, Description: "`http`: the base URL the resolver's operation targets. " +
					"`lambda`: the function (`kind: Function`) URL. `opensearch`: the domain endpoint. `eventbridge`: an " +
					"optional NATS URL (defaults to the platform NATS bus)."},
				{Name: "connection_secret", Type: tString, Description: "Name of a Secret (this API's namespace) holding " +
					"this source's credentials, kept out of the spec. `rds`: a `dsn` key with the PostgreSQL connection " +
					"string. `opensearch`: optional `username`/`password` keys for HTTP basic auth."},
			}},
			{Name: "resolvers", Type: tObjectList, Required: true, Nested: []attr{
				{Name: "type", Type: tString, Required: true, Description: "`Query` or `Mutation`."},
				{Name: "field", Type: tString, Required: true, Description: "The GraphQL field name, e.g. `getTodo`."},
				{Name: "runtime", Type: tString, Description: "The resolver runtime: `appsync-vtl` (default) or `appsync-js` (a sandboxed JS module in `request`)."},
				// Unit resolver:
				{Name: "data_source", Type: tString, Description: "Unit resolver: name of a `data_sources` entry."},
				{Name: "request", Type: tString, Description: "Unit resolver: request mapping template (VTL), or the full JS module for `appsync-js`."},
				{Name: "response", Type: tString, Description: "Unit resolver: response mapping template source."},
				// Field-level authorization (checked via a k8s SubjectAccessReview — the shared boundary):
				{Name: "auth", Type: tObject, Description: "The RBAC permission the caller must have to resolve this field. Omit for a public field.", Nested: []attr{
					{Name: "group", Type: tString, Description: "API group, e.g. `openinfra.dev`."},
					{Name: "resource", Type: tString, Description: "Resource plural, e.g. `graphqlapis`."},
					{Name: "verb", Type: tString, Description: "e.g. `get`, `create`."},
					{Name: "namespace", Type: tString, Description: "Namespace, or empty for cluster-scoped."},
					{Name: "name", Type: tString, Description: "Optional specific object name."},
				}},
				// Pipeline resolver:
				{Name: "before", Type: tString, Description: "Pipeline: before mapping template (sets `$ctx.stash` / may abort; no data source)."},
				{Name: "after", Type: tString, Description: "Pipeline: after mapping template (shapes the final value from `$ctx.prev.result`)."},
				{Name: "functions", Type: tObjectList, Nested: []attr{
					{Name: "data_source", Type: tString, Required: true, Description: "Name of a `data_sources` entry this function targets."},
					{Name: "runtime", Type: tString, Description: "Defaults to `appsync-vtl`."},
					{Name: "request", Type: tString, Required: true, Description: "The function's request mapping template."},
					{Name: "response", Type: tString, Required: true, Description: "The function's response mapping template."},
				}},
				// Per-resolver response caching (AppSync's caching behavior):
				{Name: "caching", Type: tObject, Description: "Per-resolver response caching. Ignored on `Mutation` resolvers (never cached). The caller identity is ALWAYS part of the cache key, so per-user data is never shared across callers even if `keys` omits it. In-memory (per replica) today; a shared multi-replica backend is a later rung.", Nested: []attr{
					{Name: "ttl_seconds", Type: tInt, Description: "How long a cached response lives, in seconds. 0 or unset disables caching for this resolver."},
					{Name: "keys", Type: tStringList, Description: "`$context` paths that, with the caller identity, form the cache key — e.g. `arguments.id`, `identity.sub`. Omit to fold ALL the field's arguments into the key (per-caller, per-argument-set), so distinct arguments never collide."},
				}},
			}},
			{Name: "subscriptions", Type: tObjectList, Description: "Subscription-type fields (experimental; served over graphql-transport-ws).", Nested: []attr{
				{Name: "field", Type: tString, Required: true, Description: "The Subscription field name, e.g. `onCreateTodo`."},
				{Name: "subject", Type: tString, Description: "Bus subject; defaults to `sub.<field>`."},
				{Name: "runtime", Type: tString, Description: "Defaults to `appsync-vtl`."},
				{Name: "response", Type: tString, Required: true, Description: "Response mapping that shapes each pushed event."},
				{Name: "triggered_by", Type: tStringList, Description: "Mutation field names whose success publishes to this subscription."},
				{Name: "auth", Type: tObject, Description: "Authorization checked at subscribe time (a SubjectAccessReview).", Nested: []attr{
					{Name: "group", Type: tString},
					{Name: "resource", Type: tString},
					{Name: "verb", Type: tString},
					{Name: "namespace", Type: tString},
					{Name: "name", Type: tString},
				}},
			}},
		},
		Status: []statusAttr{{Name: "url", Type: tString, Description: "In-cluster GraphQL endpoint of the engine."}},
	},
}

// connDBFields is the database-connection half of a DataFlow node. Separate from
// connSpec because a node's connection fields are all optional — a node may be a
// topic, function or bucket instead of a database.
func connDBFields() []attr {
	return []attr{
		{Name: "engine", Type: tString,
			Description: "For `role = database`: `postgres`, `mysql`, `mariadb` or `sqlserver`."},
		{Name: "host", Type: tString},
		{Name: "port", Type: tInt, Description: "Defaults to `5432`."},
		{Name: "database", Type: tString},
		{Name: "username", Type: tString},
		{Name: "password_secret_ref", Type: tObject, Nested: []attr{
			{Name: "name", Type: tString, Required: true},
			{Name: "key", Type: tString, Description: "Defaults to `password`."},
		}},
		{Name: "ssl", Type: tBool, Description: "Defaults to `false`."},
	}
}
