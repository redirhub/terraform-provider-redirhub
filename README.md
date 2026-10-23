# Terraform Provider for RedirHub

Manage RedirHub domains and links as code: review changes in a pull request, apply them from CI,
and keep every redirect of a site migration in Git.

```hcl
terraform {
  required_providers {
    redirhub = {
      source  = "redirhub/redirhub"
      version = "~> 0.1"
    }
  }
}

provider "redirhub" {} # reads REDIRHUB_API_TOKEN

resource "redirhub_link" "old_domain" {
  handler       = "redirect"
  url           = "old-domain.com"
  destinations  = ["https://new-domain.com"]
  type          = "301"
  forward_path  = true
  forward_query = true
}

resource "redirhub_link" "menu" {
  handler      = "qr"
  host         = "go.example.com"
  alias        = "menu"
  destinations = ["https://example.com/menu"]
}
```

## Resources and data sources

| Name | What it does |
| --- | --- |
| `redirhub_link` | Any link: a short link (`short-url`), a dynamic QR code (`qr`), a domain redirect (`redirect`) or a website migration mapping (`migration`). Its attributes are the fields of `/v1/links`. |
| `redirhub_domain` | A connected domain: HTTPS, short links, and the `dns_records` it needs, ready for your DNS provider's resources |
| `redirhub_domain_verification` | Waits until a domain points to RedirHub, so the links that need it come after |
| `data.redirhub_domain` | A domain connected outside Terraform |
| `data.redirhub_links` | The workspace's links, with the filters of `GET /v1/links` |

Links import by their id (`terraform import redirhub_link.old link_7bXmR4`, or an `import` block),
domains by their hostname.

## Safe to run again

Every create carries an `Idempotency-Key`: when a response is lost to a timeout, the provider sends
the request again with the same key and RedirHub returns the link it already created instead of a
second one. Rate limits (429) and server errors are retried with backoff.

## CI/CD

`examples/ci` has a GitHub Actions workflow (plan as a pull request comment, apply on merge) and a
GitLab CI pipeline. Both only need the `REDIRHUB_API_TOKEN` secret and a remote state backend.

## Development

```bash
make build      # terraform-provider-redirhub
make test       # unit tests and acceptance tests against an in-memory API; needs terraform on PATH
make testacc    # TestAccLive against a real workspace: REDIRHUB_API_TOKEN, and REDIRHUB_API_URL for a local API
make docs       # regenerate docs/ from the schema and examples/
```

To try a local build, point Terraform at it with `dev_overrides` in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides { "redirhub/redirhub" = "/path/to/terraform-provider-redirhub" }
  direct {}
}
```

Releases: push a `v*` tag; `.github/workflows/release.yml` builds, signs and publishes the release
the Terraform Registry picks up.
