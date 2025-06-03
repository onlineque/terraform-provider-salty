terraform {
  required_providers {
    salty = {
      source = "github.com/onlineque/salty"
      version = "0.1.0"
    }
  }
}

provider "salty" {
  private_key = file("~/.ssh/id_rsa")
  username    = "root"
}

resource "salty_grain" "docker" {
  server      = "servername"
  grain_key   = "roles"
  grain_value = [ "docker" ]
  apply_state = true
}
