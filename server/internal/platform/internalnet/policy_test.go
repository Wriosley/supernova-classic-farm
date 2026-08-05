package internalnet

import "testing"

func TestDefaultPolicyRequiresLoopback(t *testing.T) {
	t.Setenv(modeEnvironment, "")
	if err := RequireListenAddress("127.0.0.1:8080", "TestSvr"); err != nil {
		t.Fatal(err)
	}
	if err := RequireListenAddress("0.0.0.0:8080", "TestSvr"); err == nil {
		t.Fatal("non-loopback listen address was accepted")
	}
	if err := ValidateHTTPURL("http://zone-a:8082"); err == nil {
		t.Fatal("non-loopback URL was accepted")
	}
	if RemoteAllowed("10.0.0.2:1234") {
		t.Fatal("non-loopback remote was accepted")
	}
}

func TestKubernetesPolicyAllowsPodNetwork(t *testing.T) {
	t.Setenv(modeEnvironment, "kubernetes")
	if err := RequireListenAddress("0.0.0.0:8080", "TestSvr"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHTTPURL("http://zone-a:8082"); err != nil {
		t.Fatal(err)
	}
	if !RemoteAllowed("10.0.0.2:1234") {
		t.Fatal("pod-network remote was rejected")
	}
}
