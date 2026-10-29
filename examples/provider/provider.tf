terraform {
  required_providers {
    hibira = {
      source  = "hibira-io/hibira"
      version = "~> 0.1"
    }
  }
}

# Make a token with read and write access in the Hibira portal, then
# export HIBIRA_API_TOKEN=hkb_pat_... (or set api_token).
provider "hibira" {}
