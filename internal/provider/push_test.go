package provider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// testKeys returns a fresh APNs .p8 key and Firebase service account JSON.
func testKeys(t *testing.T) (p8, serviceAccount string) {
	t.Helper()
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(ec)
	p8 = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ = x509.MarshalPKCS8PrivateKey(rk)
	sa, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "tf-acc", "client_email": "push@tf-acc.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	return p8, string(sa)
}

func TestAccPush(t *testing.T) {
	p8, sa := testKeys(t)
	config := func(push string) string {
		return fmt.Sprintf(`
resource "hibira_app" "test" {
  name = "tf-acc-push"
}

resource "hibira_push" "test" {
  app_id = hibira_app.test.id
  %s
}`, push)
	}
	apns := fmt.Sprintf(`apns = {
    key_id      = "ABC123DEFG"
    team_id     = "TEAM123456"
    bundle_id   = "com.example.app"
    environment = "development"
    private_key = %s
  }`, strconv.Quote(p8))
	fcm := fmt.Sprintf(`fcm = { service_account = %s }`, strconv.Quote(sa))
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testPreCheck(t) },
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: config(apns),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("hibira_push.test", "instance_id", regexp.MustCompile(`.+`)),
					resource.TestMatchResourceAttr("hibira_push.test", "secret_key", regexp.MustCompile(`.+`)),
					resource.TestCheckResourceAttr("hibira_push.test", "apns.bundle_id", "com.example.app"),
					resource.TestCheckNoResourceAttr("hibira_push.test", "fcm"),
				),
			},
			{
				// Swap APNs for FCM.
				Config: config(fcm),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("hibira_push.test", "apns"),
					resource.TestCheckResourceAttrSet("hibira_push.test", "fcm.service_account"),
				),
			},
			{
				Config: config(apns + "\n  " + fcm),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_push.test", "apns.key_id", "ABC123DEFG"),
					resource.TestCheckResourceAttrSet("hibira_push.test", "fcm.service_account"),
				),
			},
			{
				ResourceName:            "hibira_push.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"apns.private_key", "fcm.service_account"},
			},
		},
	})
}
