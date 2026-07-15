package admin

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/rpcxio/libkv/store"
	"github.com/thkhxm/rpcx/v2/client"
)

type consulStateTestStore struct {
	store.Store
	pair     *store.KVPair
	getErr   error
	putErr   error
	getCalls int
	putCalls int
	putValue []byte
}

func (s *consulStateTestStore) Get(string) (*store.KVPair, error) {
	s.getCalls++
	return s.pair, s.getErr
}

func (s *consulStateTestStore) Put(_ string, value []byte, _ *store.WriteOptions) error {
	s.putCalls++
	s.putValue = append([]byte(nil), value...)
	return s.putErr
}

func activateRequest(id string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/services/"+url.PathEscape(id), nil)
	request.SetPathValue("id", id)
	return request
}

func encodedServiceID(name, address string) string {
	return base64.StdEncoding.EncodeToString([]byte(name + "^" + address))
}

func TestActivateServiceGetFailureDoesNotDereferenceNil(t *testing.T) {
	want := errors.New("injected get failure")
	kv := &consulStateTestStore{getErr: want}
	registry := &ConsulRegistry{kv: kv, baseURL: "tgf"}
	response := httptest.NewRecorder()

	registry.ActivateService(response, activateRequest(encodedServiceID("/game", "tcp@127.0.0.1:1234")))

	if response.Body.String() != "fail" {
		t.Fatalf("body = %q, want fail", response.Body.String())
	}
	if kv.putCalls != 0 {
		t.Fatalf("Put calls = %d, want 0 after Get failure", kv.putCalls)
	}
}

func TestActivateServiceRejectsInvalidIDWithoutStoreAccess(t *testing.T) {
	invalidIDs := []string{
		"not-base64",
		encodedServiceID("/..", "tcp@127.0.0.1:1234"),
		encodedServiceID("/game", ".."),
	}
	for _, id := range invalidIDs {
		kv := &consulStateTestStore{}
		registry := &ConsulRegistry{kv: kv, baseURL: "tgf"}
		response := httptest.NewRecorder()

		registry.ActivateService(response, activateRequest(id))

		if response.Body.String() != "fail" {
			t.Errorf("id %q body = %q, want fail", id, response.Body.String())
		}
		if kv.getCalls != 0 {
			t.Errorf("id %q Get calls = %d, want 0", id, kv.getCalls)
		}
	}
}

func TestActivateServiceUpdatesStateBeforeCallback(t *testing.T) {
	kv := &consulStateTestStore{pair: &store.KVPair{
		Key:   "tgf/game/tcp@127.0.0.1:1234",
		Value: []byte("state=pause&version=1.0"),
	}}
	callbackCalled := false
	registry := &ConsulRegistry{
		kv:      kv,
		baseURL: "tgf",
		StateCallBack: func(name, address string, state client.ConsulServerState) {
			callbackCalled = true
			if name != "game" || address != "tcp@127.0.0.1:1234" || state != client.ConsulServerStateActive {
				t.Errorf("callback = (%q, %q, %q)", name, address, state)
			}
		},
	}
	response := httptest.NewRecorder()

	registry.ActivateService(response, activateRequest(encodedServiceID("/game", "tcp@127.0.0.1:1234")))

	if response.Body.String() != "success" {
		t.Fatalf("body = %q, want success", response.Body.String())
	}
	if !callbackCalled {
		t.Fatal("successful state update should invoke callback")
	}
	values, err := url.ParseQuery(string(kv.putValue))
	if err != nil {
		t.Fatalf("parse stored metadata: %v", err)
	}
	if values.Get("state") != string(client.ConsulServerStateActive) {
		t.Fatalf("stored state = %q, want active", values.Get("state"))
	}
}

func TestActivateServicePutFailureSkipsCallback(t *testing.T) {
	kv := &consulStateTestStore{
		pair:   &store.KVPair{Key: "tgf/game/node", Value: []byte("state=pause")},
		putErr: errors.New("injected put failure"),
	}
	callbackCalled := false
	registry := &ConsulRegistry{
		kv:            kv,
		baseURL:       "tgf",
		StateCallBack: func(string, string, client.ConsulServerState) { callbackCalled = true },
	}
	response := httptest.NewRecorder()

	registry.ActivateService(response, activateRequest(encodedServiceID("/game", "tcp@127.0.0.1:1234")))

	if response.Body.String() != "fail" {
		t.Fatalf("body = %q, want fail", response.Body.String())
	}
	if callbackCalled {
		t.Fatal("failed Put must not invoke callback")
	}
}
