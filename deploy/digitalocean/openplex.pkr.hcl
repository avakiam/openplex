# Builds the OpenPlex 1-Click image for the DigitalOcean Marketplace: a
# snapshot of Ubuntu 24.04 with OpenPlex installed in image mode (nothing
# initialised: each Droplet makes its own secrets on first boot).
#
#   export DIGITALOCEAN_TOKEN=…        # a personal access token (write scope)
#   packer init deploy/digitalocean
#   packer build deploy/digitalocean
#
# See README.md in this folder.

packer {
  required_plugins {
    digitalocean = {
      version = ">= 1.4.0"
      source  = "github.com/digitalocean/digitalocean"
    }
  }
}

variable "do_token" {
  type      = string
  default   = env("DIGITALOCEAN_TOKEN")
  sensitive = true
}

variable "openplex_version" {
  type        = string
  default     = "latest"
  description = "OpenPlex release to install (vX.Y.Z or latest)"
}

variable "php" {
  type        = string
  default     = ""
  description = "PHP versions to build ahead, comma separated (empty: the default one)"
}

variable "region" {
  type    = string
  default = "fra1"
}

# DigitalOcean asks for the smallest Droplet as the build Droplet, so that the
# 1-Click can be created on every size.
variable "size" {
  type    = string
  default = "s-1vcpu-1gb"
}

locals {
  stamp = formatdate("YYYYMMDD-hhmm", timestamp())
}

source "digitalocean" "openplex" {
  api_token     = var.do_token
  image         = "ubuntu-24-04-x64"
  region        = var.region
  size          = var.size
  ssh_username  = "root"
  droplet_name  = "openplex-image-build"
  snapshot_name = "openplex-${var.openplex_version}-${local.stamp}"
}

build {
  sources = ["source.digitalocean.openplex"]

  provisioner "shell" {
    inline = ["cloud-init status --wait || true"]
  }

  provisioner "file" {
    source      = "${path.root}/files/"
    destination = "/"
  }

  provisioner "shell" {
    environment_vars = [
      "DEBIAN_FRONTEND=noninteractive",
      "LC_ALL=C",
      "OPENPLEX_VERSION=${var.openplex_version}",
      "OPENPLEX_PHP=${var.php}",
    ]
    scripts = [
      "${path.root}/scripts/10-openplex.sh",
      "${path.root}/scripts/20-first-login.sh",
      "${path.root}/scripts/90-digitalocean-checks.sh",
    ]
  }
}
