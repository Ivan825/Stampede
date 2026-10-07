package ai

import (
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestThirdPartyCalls(t *testing.T) {
	s, err := scenario.Parse([]byte(`
metadata: {name: shop}
target: {baseURL: https://shop.example.com}
journeys:
  - name: checkout
    steps:
      - get: /cart
      - name: pay
        post: https://api.stripe.com/v1/charges/${vars.id}
      - group: notify
        steps:
          - post: https://api.twilio.com/2010-04-01/Messages.json
      - get: https://${env.PAYMENTS_HOST}/v1/charges
      - get: https://api.sandbox.paypal.com/v1/payments
  - name: login
    steps:
      - get: https://www.google.com/recaptcha/api.js
load: {iterations: 1}`))
	if err != nil {
		t.Fatal(err)
	}
	calls := ThirdPartyCalls(s)
	var got []string
	for _, c := range calls {
		got = append(got, c.String())
	}
	want := []string{
		"journey checkout, step pay calls api.stripe.com, a payment provider",
		"journey checkout, step POST https://api.twilio.com/2010-04-01/Messages.json calls api.twilio.com, a sms provider",
		"journey login, step GET https://www.google.com/recaptcha/api.js calls www.google.com, a captcha provider",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	s.Target.BaseURL = "https://api.stripe.com"
	if calls := ThirdPartyCalls(s); len(calls) != 4 || calls[0].String() != "target.baseURL calls api.stripe.com, a payment provider" {
		t.Errorf("base URL: %v", calls)
	}
}
