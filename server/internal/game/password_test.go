package game

import "testing"

func TestEncodePassword(t *testing.T) {
	if got := encodePassword("Farm789"); got != "Idup012" {
		t.Fatalf("got %q", got)
	}
}

func TestSimpleCredentialValidation(t *testing.T) {
	if validateCredentials("student_a", "Farm123") != nil {
		t.Fatal("合法账号被拒绝")
	}
	if validateCredentials("student_a", "含中文") == nil {
		t.Fatal("错误密码被接受")
	}
}
