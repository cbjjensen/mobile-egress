package awssdk

import "testing"

func TestValidateIdentityCenterInputs(t *testing.T) {
	t.Parallel()

	if err := validateIdentityCenterInputs("https://d-1234567890.awsapps.com/start", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct{ startURL, region string }{
		{"http://d-123.awsapps.com/start", "us-east-1"},
		{"https://evil.example/start", "us-east-1"},
		{"https://d-123.awsapps.com/other", "us-east-1"},
		{"https://d-123.awsapps.com/start", "bad region"},
	} {
		if err := validateIdentityCenterInputs(value.startURL, value.region); err == nil {
			t.Fatalf("accepted Identity Center inputs %q/%q", value.startURL, value.region)
		}
	}
}
