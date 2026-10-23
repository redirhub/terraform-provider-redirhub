package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var testProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"redirhub": providerserver.NewProtocol6WithError(New("test")()),
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"old.example.com":                   "old.example.com",
		"old.example.com/":                  "old.example.com",
		"https://Old.Example.com/":          "old.example.com",
		"http://old.example.com/Blog/Post/": "old.example.com/Blog/Post",
		"OLD.example.com/a?b=1":             "old.example.com/a?b=1",
		"example.com/blog/*":                "example.com/blog/*",
	}
	for in, want := range cases {
		if got := normalizeURL(in); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// useMockAPI points the provider at an in-memory API, so these tests need no credentials.
func useMockAPI(t *testing.T) *mockAPI {
	m, server := newMockAPI(t)
	t.Setenv("REDIRHUB_API_URL", server.URL+"/v1")
	t.Setenv("REDIRHUB_API_TOKEN", "test-token")
	return m
}

const mockConfig = `
resource "redirhub_domain" "go" {
  host        = "go.example.test"
  short_links = true
}

resource "redirhub_domain_verification" "go" {
  host = redirhub_domain.go.host
}

resource "redirhub_link" "legacy" {
  handler       = "redirect"
  url           = "https://Legacy.example.test/Docs/"
  destinations  = [%[1]q]
  type          = "301"
  forward_path  = true
  title         = %[2]q
  tags          = ["migration"]
  utm           = { source = "legacy" }
}

resource "redirhub_link" "menu" {
  handler      = "qr"
  host         = redirhub_domain_verification.go.host
  alias        = "menu"
  destinations = ["https://example.com/menu"]
  qr           = { theme = %[3]q }
}

resource "redirhub_link" "spring" {
  handler      = "short-url"
  host         = redirhub_domain_verification.go.host
  destinations = ["https://example.com/spring"]
  status       = %[4]q
}

data "redirhub_links" "redirects" {
  handler    = "redirect"
  depends_on = [redirhub_link.legacy]
}
`

func TestAccMockLifecycle(t *testing.T) {
	useMockAPI(t)
	verificationPollEvery = 10 * time.Millisecond

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(mockConfig, "https://example.com/docs", "Docs", "dark", "active"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("redirhub_link.legacy", "url", "https://Legacy.example.test/Docs/"),
					resource.TestCheckResourceAttr("redirhub_link.legacy", "host", "legacy.example.test"),
					resource.TestCheckResourceAttr("redirhub_link.legacy", "status", "active"),
					resource.TestCheckNoResourceAttr("redirhub_link.legacy", "alias"),
					resource.TestCheckResourceAttr("redirhub_link.menu", "url", "go.example.test/menu"),
					resource.TestCheckResourceAttr("redirhub_link.menu", "qr.theme", "dark"),
					resource.TestCheckResourceAttr("redirhub_link.menu", "qr.margin", "m"),
					resource.TestCheckResourceAttrSet("redirhub_link.spring", "alias"),
					resource.TestCheckResourceAttr("redirhub_domain.go", "dns_records.#", "2"),
					resource.TestCheckResourceAttr("redirhub_domain.go", "dns_records.0.type", "CNAME"),
					resource.TestCheckResourceAttr("data.redirhub_links.redirects", "links.#", "1"),
					resource.TestCheckResourceAttr("data.redirhub_links.redirects", "links.0.handler", "redirect"),
				),
			},
			{
				Config: fmt.Sprintf(mockConfig, "https://example.com/handbook", "Handbook", "light", "paused"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("redirhub_link.legacy", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("redirhub_link.menu", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("redirhub_link.spring", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("redirhub_link.legacy", "destinations.0", "https://example.com/handbook"),
					resource.TestCheckResourceAttr("redirhub_link.menu", "qr.theme", "light"),
					resource.TestCheckResourceAttr("redirhub_link.spring", "status", "paused"),
				),
			},
			{
				ResourceName:            "redirhub_link.legacy",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"url"},
			},
			{
				ResourceName:      "redirhub_link.menu",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "redirhub_domain.go",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccMockFieldsRemoved clears fields the config no longer sets, the UTM parameters included,
// which the API only takes as an empty set.
func TestAccMockFieldsRemoved(t *testing.T) {
	useMockAPI(t)
	link := func(extra string) string {
		return fmt.Sprintf(`
resource "redirhub_link" "old" {
  handler      = "redirect"
  url          = "old.example.test"
  destinations = ["https://example.com"]
  %s
}`, extra)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{Config: link(`title = "Old"` + "\n" + `tags = ["a"]` + "\n" + `utm = { source = "x" }`)},
			{
				Config: link(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("redirhub_link.old", "title"),
					resource.TestCheckNoResourceAttr("redirhub_link.old", "tags"),
					resource.TestCheckNoResourceAttr("redirhub_link.old", "utm"),
				),
			},
		},
	})
}

// TestAccMockRetryCreatesOnce loses the response of a create, as a timeout would: the client
// sends the same Idempotency-Key again and gets the first link back instead of a second one.
func TestAccMockRetryCreatesOnce(t *testing.T) {
	m := useMockAPI(t)
	m.failNextCreate = true

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{{
			Config: `
resource "redirhub_link" "old" {
  handler      = "redirect"
  url          = "old.example.test"
  destinations = ["https://example.com"]
}`,
			Check: func(*terraform.State) error {
				if m.creates != 1 || len(m.links) != 1 {
					return fmt.Errorf("want 1 link created once, got %d links from %d creates", len(m.links), m.creates)
				}
				return nil
			},
		}},
	})
}

func TestAccMockValidatesFieldsPerHandler(t *testing.T) {
	useMockAPI(t)
	cases := map[string]string{
		`handler = "redirect"` + "\n" + `destinations = ["https://example.com"]`:                                                                    "Missing url",
		`handler = "short-url"` + "\n" + `destinations = ["https://example.com"]`:                                                                   "Missing host",
		`handler = "redirect"` + "\n" + `url = "a.example.test"` + "\n" + `alias = "x"` + "\n" + `destinations = ["https://example.com"]`:           "alias is not set on a redirect",
		`handler = "qr"` + "\n" + `host = "go.example.test"` + "\n" + `url = "go.example.test/x"` + "\n" + `destinations = ["https://example.com"]`: "url is not set on a short link",
	}
	for body, message := range cases {
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testProviders,
			Steps: []resource.TestStep{{
				Config:      "resource \"redirhub_link\" \"x\" {\n" + body + "\n}",
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(message),
			}},
		})
	}
}

// TestAccLive runs against a real workspace: REDIRHUB_API_TOKEN, and REDIRHUB_API_URL for a
// local API. It is skipped without a token.
func TestAccLive(t *testing.T) {
	if os.Getenv("REDIRHUB_API_TOKEN") == "" {
		t.Skip("REDIRHUB_API_TOKEN is not set")
	}
	suffix := time.Now().Format("150405")
	source := fmt.Sprintf("https://Legacy-acc-%s.example.test/", suffix)
	config := func(title string) string {
		return fmt.Sprintf(`
resource "redirhub_link" "legacy" {
  handler      = "redirect"
  url          = %q
  destinations = ["https://www.example.com/"]
  type         = "301"
  title        = %q
  utm          = { source = "acc" }
}`, source, title)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{Config: config("First"), Check: resource.TestCheckResourceAttr("redirhub_link.legacy", "url", source)},
			{Config: config("Second"), Check: resource.TestCheckResourceAttr("redirhub_link.legacy", "title", "Second")},
			{ResourceName: "redirhub_link.legacy", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"url"}},
		},
	})
}
