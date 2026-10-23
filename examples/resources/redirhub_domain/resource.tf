resource "redirhub_domain" "go" {
  host        = "go.example.com"
  short_links = true
}

# Point the domain to RedirHub in the same apply, here with Cloudflare.
resource "cloudflare_dns_record" "redirhub" {
  for_each = { for r in redirhub_domain.go.dns_records : "${r.type}-${r.name}" => r }

  zone_id = var.cloudflare_zone_id
  name    = each.value.name
  type    = each.value.type
  content = each.value.value
  ttl     = 300
  proxied = false
}
