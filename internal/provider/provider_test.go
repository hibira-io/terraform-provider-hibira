package provider

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// Acceptance tests (TF_ACC=1) run against a real Hibira: set
// HIBIRA_API_TOKEN (a read and write token) and, for a local server,
// HIBIRA_ENDPOINT. They create and delete apps in that account.
var testProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"hibira": providerserver.NewProtocol6WithError(New("test")()),
}

func testPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("HIBIRA_API_TOKEN") == "" {
		t.Fatal("HIBIRA_API_TOKEN must be set for acceptance tests")
	}
}

func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// testAppWebhooks checks how many webhook endpoints the app has in Hibira.
func testAppWebhooks(name string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id := s.RootModule().Resources[name].Primary.ID
		app, err := client.New(os.Getenv("HIBIRA_ENDPOINT"), os.Getenv("HIBIRA_API_TOKEN"), "").App(context.Background(), id)
		if err != nil {
			return err
		}
		if len(app.Webhooks) != want {
			return errorf("app %s has %d webhooks, want %d", id, len(app.Webhooks), want)
		}
		return nil
	}
}
