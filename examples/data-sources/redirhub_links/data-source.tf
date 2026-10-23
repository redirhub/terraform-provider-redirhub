# The redirects on a domain, e.g. to find the ids of links made in the dashboard before importing them.
data "redirhub_links" "old_site" {
  handler = "redirect,migration"
  host    = "old.example.com"
}

output "old_site_links" {
  value = { for l in data.redirhub_links.old_site.links : l.url => l.id }
}
