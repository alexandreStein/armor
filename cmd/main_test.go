package cmd

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/alexandrestein/securelink"
)

var (
	testCA, serverCert *securelink.Certificate
)

// Generates the certificates
func genCert(ctx context.Context) {
	conf := securelink.NewDefaultCertificationConfigWithDefaultTemplate("ca")
	conf.KeyType = securelink.KeyTypeEc
	conf.KeyLength = securelink.KeyLengthEc256
	testCA, _ = securelink.NewCA(conf, "ca")

	conf = securelink.NewDefaultCertificationConfigWithDefaultTemplate("localhost")
	conf.KeyType = securelink.KeyTypeEc
	conf.KeyLength = securelink.KeyLengthEc256
	// Build the server certificate
	serverCert, _ = testCA.NewCert(conf, "localhost")

	// Extracting certificate and key
	ioutil.WriteFile("cert.pem", serverCert.GetCertPEM(), 0777)
	defer os.RemoveAll("cert.pem")
	ioutil.WriteFile("key.pem", serverCert.GetKeyPEM(), 0777)
	defer os.RemoveAll("key.pem")

	select {
	case <-ctx.Done():
		return
	}
}

func TestMain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*500)
	go genCert(ctx)

	configFile = "./testing/config.yaml"
	time.Sleep(time.Millisecond * 500)

	go Execute()

	time.Sleep(time.Millisecond * 500)
	cancel()

	cli := securelink.NewHTTPSConnector("localhost", testCA)
	resp, err := cli.Get("https://localhost/testing/config.yaml")
	if err != nil {
		t.Log(err)
		t.Fail()
		return
	}
	if resp.StatusCode != 200 {
		t.Log("not 200 response", resp.StatusCode)
		t.Log("header", resp.Header)
		t.Log("req", resp.Request)
		t.Fail()
		return
	}
}
