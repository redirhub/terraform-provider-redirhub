resource "redirhub_domain" "go" {
  host        = "go.example.com"
  short_links = true
}

resource "cloudflare_dns_record" "redirhub" {
  for_each = { for r in redirhub_domain.go.dns_records : "${r.type}-${r.name}" => r }

  zone_id = var.cloudflare_zone_id
  name    = each.value.name
  type    = each.value.type
  content = each.value.value
  ttl     = 300
}

# Waits until the records above are live, so the links below are served from the first request.
resource "redirhub_domain_verification" "go" {
  host       = redirhub_domain.go.host
  timeout    = "15m"
  depends_on = [cloudflare_dns_record.redirhub]
}

resource "redirhub_link" "spring" {
  handler      = "short-url"
  host         = redirhub_domain_verification.go.host
  alias        = "spring"
  destinations = ["https://example.com/spring"]
}
