package internal

// D7 单元测试：HMAC login token 的签发与校验（表驱动覆盖全部错误分支）。

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("unit-test-secret-32-bytes-long!!")

func TestLoginToken_RoundTrip(t *testing.T) {
	cases := []string{
		"u1",
		"user-with-dash",
		"含中文用户",
		"user.with.dots", // userId 含点号不能破坏三段解析（base64url 编码保证）
		"1234567890",
	}
	for _, uid := range cases {
		t.Run(uid, func(t *testing.T) {
			token, err := BuildLoginToken(testSecret, uid, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatalf("BuildLoginToken: %v", err)
			}
			got, err := VerifyLoginToken(testSecret, token, time.Now())
			if err != nil {
				t.Fatalf("VerifyLoginToken: %v", err)
			}
			if got != uid {
				t.Errorf("verified uid = %q, want %q", got, uid)
			}
		})
	}
}

func TestLoginToken_ErrorPaths(t *testing.T) {
	now := time.Now()
	valid, err := BuildLoginToken(testSecret, "u1", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("BuildLoginToken: %v", err)
	}
	expired, err := BuildLoginToken(testSecret, "u1", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("BuildLoginToken(expired): %v", err)
	}
	// 篡改过期时间（签名随之失效）
	parts := strings.Split(valid, ".")
	extended := parts[0] + ".99999999999." + parts[2]

	cases := []struct {
		name    string
		secret  []byte
		token   string
		wantErr error
	}{
		{"空密钥", nil, valid, ErrLoginTokenSecretEmpty},
		{"空token", testSecret, "", ErrLoginTokenMalformed},
		{"段数不足", testSecret, "abc.123", ErrLoginTokenMalformed},
		{"过期", testSecret, expired, ErrLoginTokenExpired},
		{"密钥不一致", []byte("another-secret"), valid, ErrLoginTokenSignature},
		{"篡改过期时间", testSecret, extended, ErrLoginTokenSignature},
		{"篡改签名", testSecret, valid[:len(valid)-2] + "zz", ErrLoginTokenSignature},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := VerifyLoginToken(c.secret, c.token, now)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestBuildLoginToken_Validation(t *testing.T) {
	if _, err := BuildLoginToken(nil, "u1", time.Now()); !errors.Is(err, ErrLoginTokenSecretEmpty) {
		t.Errorf("nil secret err = %v, want ErrLoginTokenSecretEmpty", err)
	}
	if _, err := BuildLoginToken(testSecret, "", time.Now()); !errors.Is(err, ErrLoginTokenEmptyUserId) {
		t.Errorf("empty uid err = %v, want ErrLoginTokenEmptyUserId", err)
	}
}
