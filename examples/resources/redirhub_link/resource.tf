# A domain redirect.
resource "redirhub_link" "old_domain" {
  handler       = "redirect"
  url           = "old-domain.com"
  destinations  = ["https://new-domain.com"]
  type          = "301"
  forward_path  = true
  forward_query = true
}

# A website migration: one mapping per old page, kept in a CSV next to the config.
resource "redirhub_link" "migration" {
  for_each = { for row in csvdecode(file("${path.module}/redirects.csv")) : row.from => row }

  handler      = "migration"
  url          = each.value.from
  destinations = [each.value.to]
  type         = "301"
}

# A branded short link.
resource "redirhub_link" "spring" {
  handler      = "short-url"
  host         = "go.example.com"
  alias        = "spring"
  destinations = ["https://example.com/campaigns/spring"]
  utm = {
    source   = "newsletter"
    campaign = "spring"
  }
}

# A dynamic QR code, paused until the menu is printed.
resource "redirhub_link" "menu" {
  handler      = "qr"
  host         = "go.example.com"
  alias        = "menu"
  destinations = ["https://example.com/menu"]
  status       = "paused"
  qr = {
    theme = "dark"
    logo  = true
  }
}
