package cloud

import (
	"context"
	"testing"

	"mobile-egress/windows-client/internal/securestore"
)

func TestEncryptedRepositoryPersistsAccessKeysAndManagedNodes(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	credentials := StoredAccessKeys{AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "secret", SessionToken: "token"}
	if err := repository.SaveAccessKeys(context.Background(), credentials); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.AccessKeys(context.Background())
	if err != nil || loaded != credentials {
		t.Fatalf("AccessKeys() = %#v/%v", loaded, err)
	}
	node := ManagedNode{
		InstanceID: "i-0123456789abcdef0", ClientSerial: "A1", ConfigurationPublicKey: "public",
		ServiceVersion: "1.2.3", Health: "healthy", SOCKSUsername: "user", SOCKSPassword: "password", SOCKSPort: 1080,
		RelayURL: "https://bridge.tail123.ts.net:8443", CertificatePEM: "certificate", CACertificatePEM: "ca",
	}
	if err := repository.SaveNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	views, err := repository.NodeViews(context.Background())
	if err != nil || len(views) != 1 || views[0].InstanceID != node.InstanceID || views[0].Proxy != "socks5://***:***@127.0.0.1:1080" {
		t.Fatalf("NodeViews() = %#v/%v", views, err)
	}
	proxy, err := repository.ProxyLine(context.Background(), node.InstanceID)
	if err != nil || proxy != "socks5://user:password@127.0.0.1:1080" {
		t.Fatalf("ProxyLine() = %q/%v", proxy, err)
	}
}
