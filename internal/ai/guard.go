package ai

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// The third-party guard keeps generated scenarios (and their dry runs) away
// from services that cost money, message real people or exist to stop
// automated traffic. A load test must never charge cards, send SMS or
// hammer a CAPTCHA provider, even if the app under test calls them in
// production. The list is deliberately small and curated; see docs/ai.md.
//
// Test-mode hosts (payment sandboxes) are allowed, but only when the user
// lists them as allowed hosts, like any other host that is not the target.

type guardEntry struct {
	host     string
	category string
}

// blockedHosts match the host itself and every subdomain.
var blockedHosts = []guardEntry{
	// Payments (live endpoints).
	{"api.stripe.com", "payment"}, {"checkout.stripe.com", "payment"}, {"js.stripe.com", "payment"},
	{"api.paypal.com", "payment"}, {"api-m.paypal.com", "payment"}, {"www.paypal.com", "payment"},
	{"api.braintreegateway.com", "payment"}, {"payments.braintree-api.com", "payment"},
	{"connect.squareup.com", "payment"}, {"api.squareup.com", "payment"},
	{"checkout-live.adyen.com", "payment"}, {"pal-live.adyen.com", "payment"}, {"adyenpayments.com", "payment"},
	{"api.checkout.com", "payment"}, {"api.razorpay.com", "payment"}, {"api.mollie.com", "payment"},
	{"api.authorize.net", "payment"}, {"secure.authorize.net", "payment"}, {"api.klarna.com", "payment"},
	// SMS, voice and messaging.
	{"api.twilio.com", "sms"}, {"verify.twilio.com", "sms"}, {"messaging.twilio.com", "sms"},
	{"rest.nexmo.com", "sms"}, {"api.nexmo.com", "sms"}, {"api.vonage.com", "sms"},
	{"rest.messagebird.com", "sms"}, {"api.plivo.com", "sms"}, {"api.sinch.com", "sms"},
	{"api.telnyx.com", "sms"}, {"textbelt.com", "sms"},
	// Email delivery.
	{"api.sendgrid.com", "email"}, {"api.mailgun.net", "email"}, {"api.postmarkapp.com", "email"},
	{"api.resend.com", "email"}, {"api.sparkpost.com", "email"},
	// CAPTCHA and bot protection.
	{"recaptcha.net", "captcha"}, {"www.recaptcha.net", "captcha"}, {"www.google.com", "captcha"},
	{"hcaptcha.com", "captcha"}, {"challenges.cloudflare.com", "captcha"},
	{"arkoselabs.com", "captcha"}, {"funcaptcha.com", "captcha"}, {"2captcha.com", "captcha"},
}

// testModeHosts are payment sandboxes. They are never blocked by the guard
// (the host policy still requires listing them as allowed hosts).
var testModeHosts = []string{
	"api-m.sandbox.paypal.com", "api.sandbox.paypal.com", "www.sandbox.paypal.com",
	"sandbox.braintreegateway.com", "payments.sandbox.braintree-api.com",
	"connect.squareupsandbox.com", "checkout-test.adyen.com", "pal-test.adyen.com",
	"api.sandbox.checkout.com", "apitest.authorize.net", "test.authorize.net",
	"api.playground.klarna.com",
}

func matchHost(host, entry string) bool {
	return host == entry || strings.HasSuffix(host, "."+entry)
}

// ThirdPartyCategory returns the category of a host on the guard list
// ("payment", "sms", "email" or "captcha"), or "" when the host is not
// blocked. Test-mode hosts are never blocked.
func ThirdPartyCategory(host string) string {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	for _, t := range testModeHosts {
		if matchHost(h, t) {
			return ""
		}
	}
	for _, b := range blockedHosts {
		if matchHost(h, b.host) {
			return b.category
		}
	}
	return ""
}

// ThirdPartyCall is a place in a scenario that sends traffic to a host on
// the guard list.
type ThirdPartyCall struct {
	// Journey and Step locate the call; both are empty for the target's
	// base URL.
	Journey  string `json:"journey,omitempty"`
	Step     string `json:"step,omitempty"`
	Host     string `json:"host"`
	Category string `json:"category"`
}

func (c ThirdPartyCall) String() string {
	where := "target.baseURL"
	if c.Journey != "" {
		where = "journey " + c.Journey
		if c.Step != "" {
			where += ", step " + c.Step
		}
	}
	return fmt.Sprintf("%s calls %s, a %s provider", where, c.Host, c.Category)
}

// ThirdPartyCalls lists the steps of s (and its base URL) that send
// traffic to a payment, SMS, email or CAPTCHA provider on the guard list.
// Hosts written as templates cannot be known before a run and are not
// reported; the run's host policy still applies to them.
func ThirdPartyCalls(s *scenario.Scenario) []ThirdPartyCall {
	var out []ThirdPartyCall
	hostOf := func(raw string) string {
		raw = strings.TrimSpace(raw)
		i := strings.Index(raw, "://")
		if i <= 0 {
			return ""
		}
		// Templates become a marker so the rest of the URL parses; a host
		// that is (partly) a template is unknown until the run.
		const marker = "stampede-template"
		u, err := url.Parse(strings.ReplaceAll(maskTemplates(raw), "\x00", marker))
		if err != nil {
			return ""
		}
		h := strings.ToLower(u.Hostname())
		if h == "" || strings.Contains(h, marker) {
			return ""
		}
		return h
	}
	if h := hostOf(s.Target.BaseURL); h != "" {
		if cat := ThirdPartyCategory(h); cat != "" {
			out = append(out, ThirdPartyCall{Host: h, Category: cat})
		}
	}
	for _, j := range s.Journeys {
		seen := map[string]bool{}
		scenario.WalkSteps(j.Steps, func(st scenario.Step) {
			raw, ok := scenario.StepURL(st)
			if !ok {
				return
			}
			h := hostOf(raw)
			if h == "" || seen[h+"\x00"+st.Name] {
				return
			}
			if cat := ThirdPartyCategory(h); cat != "" {
				seen[h+"\x00"+st.Name] = true
				name := st.Name
				if name == "" && st.Kind == scenario.StepRequest && st.Request != nil {
					name = st.Request.Method + " " + raw // the default name requests get
				}
				out = append(out, ThirdPartyCall{Journey: j.Name, Step: name, Host: h, Category: cat})
			}
		})
	}
	return out
}

// GuardList returns the blocked and test-mode hosts, for documentation and
// the CLI's help output.
func GuardList() (blocked map[string]string, testMode []string) {
	blocked = make(map[string]string, len(blockedHosts))
	for _, b := range blockedHosts {
		blocked[b.host] = b.category
	}
	return blocked, append([]string(nil), testModeHosts...)
}
