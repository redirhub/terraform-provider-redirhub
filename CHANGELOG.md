## 0.1.0

First release.

- `redirhub_link`: short links, dynamic QR codes, domain redirects and website migration mappings, as one resource with the fields of `/v1/links`, imported by link id.
- `redirhub_domain`: connect a domain, turn HTTPS, Force HTTPS and short links on or off, and read the DNS records it needs.
- `redirhub_domain_verification`: wait until a domain points to RedirHub.
- Data sources `redirhub_domain` and `redirhub_links`.
- Creates are safe to retry: each carries an `Idempotency-Key`. Rate limits and server errors are retried with backoff.
