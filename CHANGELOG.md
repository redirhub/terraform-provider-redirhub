## Unreleased

- `redirhub_domain`: `connection_mode` (`managed`, the default, or `self_hosted`) connects a domain through your own servers with Self-Hosted TLS, and switches it in place. A self-hosted domain lists no records of ours in `dns_records` (a wildcard lists its `_acme-challenge` CNAME), and `dns_ok` and `redirhub_domain_verification` wait for your servers' answer instead of DNS pointing to RedirHub.
- Data source `redirhub_domain`: `connection_mode`.

## 0.1.0

First release.

- `redirhub_link`: short links, dynamic QR codes, domain redirects and website migration mappings, as one resource with the fields of `/v1/links`, imported by link id.
- `redirhub_domain`: connect a domain, turn HTTPS, Force HTTPS and short links on or off, and read the DNS records it needs.
- `redirhub_domain_verification`: wait until a domain points to RedirHub.
- Data sources `redirhub_domain` and `redirhub_links`.
- Creates are safe to retry: each carries an `Idempotency-Key`. Rate limits and server errors are retried with backoff.
