data "redirhub_domain" "go" {
  host = "go.example.com"
}

output "points_to_redirhub" {
  value = data.redirhub_domain.go.dns_ok
}
