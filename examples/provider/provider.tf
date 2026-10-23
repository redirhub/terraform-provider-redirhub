terraform {
  required_providers {
    redirhub = {
      source  = "redirhub/redirhub"
      version = "~> 0.1"
    }
  }
}

# The token comes from REDIRHUB_API_TOKEN; create one under Account → API tokens.
provider "redirhub" {}
