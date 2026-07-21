// A fuller example: the kinds served by the table-driven resources.
//
// This is deliberately one file rather than a module, so it reads top to bottom as
// "here is what the provider can express". Everything here is namespaced; add
// `namespace = "..."` to place resources somewhere other than `default`.
//
// See examples/main.tf for the smaller application / database / VM example.

terraform {
  required_providers {
    openinfra = {
      source  = "harn3ss/openinfra"
      version = "~> 0.1"
    }
  }
}

provider "openinfra" {
  # Defaults to in-cluster credentials, else $KUBECONFIG / ~/.kube/config.
}

# ── Storage ────────────────────────────────────────────────────────────────────

# A block volume for a VM to attach. `migratable` puts it on the RWX-block class so
# the VM can still live-migrate; it is fixed at creation, so changing it replaces.
resource "openinfra_volume" "data" {
  name       = "orders-data"
  size       = "50Gi"
  migratable = true
}

# Restore a second volume from a snapshot instead of creating it empty.
resource "openinfra_volume" "restored" {
  name = "orders-data-restored"
  size = "50Gi"

  source = {
    snapshot = "orders-data-2026-07-21"
  }
}

# A shared filesystem over SMB, for machines that need a common mount.
resource "openinfra_file_share" "shared" {
  name   = "team-share"
  size   = "200Gi"
  expose = true
}

# ── Network ────────────────────────────────────────────────────────────────────

# Firewall rules. Members are default-deny inbound once this is attached, so the
# ingress list is the complete set of ways in.
resource "openinfra_security_group" "database" {
  name = "database-access"

  ingress = [{
    protocol    = "TCP"
    description = "Postgres from the application namespace only"
    ports       = [5432]
    from = [
      { namespace = "apps" },
    ]
  }]

  egress = [{
    protocol    = "TCP"
    description = "Outbound HTTPS for extension downloads"
    ports       = [443]
    to          = [{ cidr = "0.0.0.0/0" }]
  }]
}

# ── Serverless ─────────────────────────────────────────────────────────────────

# A function that scales to zero between requests.
resource "openinfra_function" "resize" {
  name  = "image-resize"
  image = "ghcr.io/harn3ss/image-resize:latest"

  scaling = {
    min    = 0
    max    = 20
    target = 50
  }

  env = [
    { name = "OUTPUT_BUCKET", value = "thumbnails" },
  ]

  security_groups = [openinfra_security_group.database.name]
}

# A GPU-backed function, for serverless inference.
resource "openinfra_function" "embed" {
  name  = "embeddings"
  image = "ghcr.io/harn3ss/embed:latest"
  gpu   = 1

  scaling = {
    min = 0
    max = 2
  }
}

# A served language model with an OpenAI-compatible endpoint.
resource "openinfra_model" "chat" {
  name   = "chat"
  model  = "llama3.1:8b"
  expose = true
}

# ── Data movement ──────────────────────────────────────────────────────────────

# Change data capture out of a database and into NATS JetStream.
resource "openinfra_stream" "orders_cdc" {
  name = "orders-cdc"

  source = {
    engine   = "postgres"
    host     = "orders-db-rw"
    database = "orders"
    username = "app"
    tables   = ["public.orders", "public.order_items"]

    # The password is never in this resource — only a reference to the Secret
    # holding it. open-infra generates that Secret when it creates the database.
    password_secret_ref = {
      name = "orders-db-app"
      key  = "password"
    }
  }
}

# Deliver those change events to a function as HTTP POSTs.
resource "openinfra_function" "on_order" {
  name  = "on-order"
  image = "ghcr.io/harn3ss/on-order:latest"

  trigger = {
    stream = openinfra_stream.orders_cdc.name
  }
}

# A one-way load from an external database into a managed one — the DMS shape.
resource "openinfra_migration" "legacy" {
  name = "legacy-orders"
  mode = "full-load-and-cdc"

  source = {
    engine              = "mysql"
    host                = "legacy-db.internal"
    port                = 3306
    database            = "orders"
    username            = "readonly"
    password_secret_ref = { name = "legacy-db-creds" }
  }

  target = {
    engine              = "postgres"
    host                = "orders-db-rw"
    database            = "orders"
    username            = "app"
    password_secret_ref = { name = "orders-db-app" }
  }
}

# ── Query ──────────────────────────────────────────────────────────────────────

# A one-shot query over object storage. This is a JOB, not a desired state:
# changing `sql` runs a new query rather than altering the old one, so the field
# forces replacement.
resource "openinfra_query" "daily_revenue" {
  name   = "daily-revenue"
  engine = "duckdb"

  sql = <<-SQL
    SELECT date_trunc('day', created_at) AS day, sum(total) AS revenue
    FROM read_parquet('s3://lakehouse/orders/*.parquet')
    GROUP BY 1 ORDER BY 1 DESC
  SQL
}

# ── Windows ────────────────────────────────────────────────────────────────────

# A golden Windows image for VMs to clone. Building one takes a long time and is
# not idempotent, so every field replaces.
resource "openinfra_vm_image" "win2022" {
  name      = "windows-server-2022"
  os        = "windows-server-2022"
  disk_size = "64Gi"
}

# An Active Directory domain controller.
resource "openinfra_directory" "corp" {
  name   = "corp"
  domain = "corp.example.lan"
  expose = true
}

# ── Outputs ────────────────────────────────────────────────────────────────────

output "resize_url" {
  value = openinfra_function.resize.url
}

output "chat_endpoint" {
  value = openinfra_model.chat.endpoint
}

output "share_path" {
  value = openinfra_file_share.shared.share
}

output "daily_revenue_result" {
  description = "s3:// URI of the CSV result, once the query has finished."
  value       = openinfra_query.daily_revenue.result_location
}
