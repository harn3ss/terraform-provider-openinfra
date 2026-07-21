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
  # kubeconfig = "~/.kube/config"
  # context    = "my-cluster"
}

# A container workload: Deployment + Service + Ingress + HPA.
resource "openinfra_application" "web" {
  name   = "hello-web"
  image  = "ghcr.io/harn3ss/hello-web:latest"
  port   = 8080
  expose = true
}

# A managed database. `connection_secret` is the generated Secret holding
# credentials — read it with the kubernetes provider to wire them elsewhere.
resource "openinfra_database" "orders" {
  name   = "orders"
  engine = "postgres"
}

# A virtual machine. high_availability puts the root disk on Longhorn, which is
# required if you want to snapshot it.
resource "openinfra_virtual_machine" "dc" {
  name              = "windowsdc"
  os                = "windows-server-2022"
  cpu               = 4
  memory            = "8Gi"
  high_availability = true
  running           = true
}

output "orders_connection_secret" {
  value = openinfra_database.orders.connection_secret
}

output "dc_ip" {
  value = openinfra_virtual_machine.dc.ip
}
