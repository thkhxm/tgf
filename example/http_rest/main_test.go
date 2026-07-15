package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserStoreHandlers(t *testing.T) {
	store := new(userStore)

	createReq := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"tim"}`))
	createRec := httptest.NewRecorder()
	store.createUser(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created userDTO
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID != 1 || created.Name != "tim" {
		t.Fatalf("created=%+v", created)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	getReq.SetPathValue("id", "42")
	getRec := httptest.NewRecorder()
	store.getUser(getRec, getReq)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), `"name":"user-42"`) {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestCreateUserRejectsEmptyName(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":""}`))
	rec := httptest.NewRecorder()
	new(userStore).createUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
