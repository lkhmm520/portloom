package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

const (
	gateReadPath       = "/api/v1/cutover/gates/read"
	gateTransitionPath = "/api/v1/cutover/gates/transition"
	gateAdminToken     = "gate-admin-secret"
	gateManagementHost = "console.example.test"
	gateA              = "a.example.test"
	gateB              = "b.example.test"
)

type testGate struct {
	Host        string `json:"host"`
	Phase       string `json:"phase"`
	Generation  int64  `json:"generation"`
	OperationID string `json:"operation_id"`
	ClientID    string `json:"client_id"`
}

func gateAgentFixture(t *testing.T, s *store.Store, name string) (string, string) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateEnrollmentToken(ctx, name, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, agentToken, err := s.ConsumeEnrollmentToken(ctx, name, name)
	if err != nil {
		t.Fatal(err)
	}
	return agent.ID, agentToken
}

func gateRouteFixture(t *testing.T, s *store.Store, client, host, prefix string, protocol domain.Protocol, port int) {
	t.Helper()
	if _, err := s.CreateRoute(context.Background(), domain.Route{ClientID: client, Name: host, Domain: host, PathPrefix: prefix, Protocol: protocol, PublicPort: port, LocalHost: "localhost", LocalPort: 8080, Enabled: true}); err != nil {
		t.Fatal(err)
	}
}

func gateFixture(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	s := openTestStore(t)
	client, token := gateAgentFixture(t, s, "gate-client")
	for _, host := range []string{gateA, gateB} {
		gateRouteFixture(t, s, client, host, "", domain.ProtocolHTTPS, 0)
	}
	return s, client, token
}

func gateHTTP(h http.Handler, path, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func gateJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func gateRequest(expect []testGate, phase, operationID, clientID string) map[string]any {
	return map[string]any{"expect": expect, "next": map[string]string{"phase": phase, "operation_id": operationID, "client_id": clientID}}
}

func gateCheckStatus(t *testing.T, res *httptest.ResponseRecorder, want int) {
	t.Helper()
	if res.Code != want {
		t.Fatalf("status=%d want=%d body=%s", res.Code, want, res.Body.String())
	}
}

func TestCutoverGateAPIDurableCAS(t *testing.T) {
	s, client, _ := gateFixture(t)
	h := New(s, Config{AdminToken: gateAdminToken, PublicHost: gateManagementHost, EnableCutover: true})
	read := func(want ...testGate) []testGate {
		t.Helper()
		res := gateHTTP(h, gateReadPath, gateAdminToken, []byte(`{"hosts":[" B.Example.Test. ","a.example.test"]}`))
		gateCheckStatus(t, res, http.StatusOK)
		var body struct {
			Gates []testGate `json:"gates"`
		}
		decodeResponse(t, res, &body)
		if len(body.Gates) != len(want) {
			t.Fatalf("gates=%+v want=%+v", body.Gates, want)
		}
		for i, gate := range body.Gates {
			if gate != want[i] {
				t.Fatalf("gate=%+v want=%+v", gate, want[i])
			}
			assertStoredGate(t, s, gate.Host, store.PublicationGate{
				Generation:        gate.Generation,
				PublicationPolicy: store.PublicationPolicy{Phase: store.PublicationPhase(gate.Phase), OperationID: gate.OperationID, ClientID: gate.ClientID},
			})
		}
		return body.Gates
	}
	transition := func(expect []testGate, phase string, want int) {
		t.Helper()
		res := gateHTTP(h, gateTransitionPath, gateAdminToken, gateJSON(t, gateRequest(expect, phase, "cutover-1", client)))
		gateCheckStatus(t, res, want)
		if want == http.StatusNoContent && res.Body.Len() != 0 {
			t.Fatalf("unexpected body: %s", res.Body.String())
		}
	}
	open := read(testGate{Host: gateA}, testGate{Host: gateB})
	transition(open, "frozen", http.StatusNoContent)
	transition([]testGate{open[1], open[0]}, "frozen", http.StatusNoContent)
	frozen := read(testGate{gateA, "frozen", 1, "cutover-1", client}, testGate{gateB, "frozen", 1, "cutover-1", client})
	transition(open, "blocked", http.StatusConflict)
	transition(frozen, "blocked", http.StatusNoContent)
	blocked := read(testGate{gateA, "blocked", 2, "cutover-1", client}, testGate{gateB, "blocked", 2, "cutover-1", client})
	transition(blocked, "", http.StatusNoContent)
	transition(blocked, "", http.StatusNoContent)
	read(testGate{Host: gateA, Generation: 3}, testGate{Host: gateB, Generation: 3})
	transition(open, "frozen", http.StatusConflict)
}

func TestCutoverGateAPIRequiresOptInAndAdminBearer(t *testing.T) {
	s, client, agentToken := gateFixture(t)
	read := []byte(`{"hosts":["a.example.test"]}`)
	transition := gateJSON(t, gateRequest([]testGate{{Host: gateA}}, "blocked", "op", client))
	for _, tc := range []struct {
		name   string
		config Config
		token  string
		status int
	}{
		{"default off", Config{PublicHost: gateManagementHost, AdminToken: gateAdminToken}, gateAdminToken, 404},
		{"missing management host", Config{EnableCutover: true, AdminToken: gateAdminToken}, gateAdminToken, 404},
		{"invalid management host", Config{EnableCutover: true, PublicHost: "console.example.test:443", AdminToken: gateAdminToken}, gateAdminToken, 404},
		{"missing admin configuration", Config{EnableCutover: true, PublicHost: gateManagementHost}, "", 401},
		{"no bearer", Config{EnableCutover: true, PublicHost: gateManagementHost, AdminToken: gateAdminToken}, "", 401},
		{"invalid bearer", Config{EnableCutover: true, PublicHost: gateManagementHost, AdminToken: gateAdminToken}, "wrong", 401},
		{"agent bearer", Config{EnableCutover: true, PublicHost: gateManagementHost, AdminToken: gateAdminToken}, agentToken, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(s, tc.config)
			gateCheckStatus(t, gateHTTP(h, gateReadPath, tc.token, read), tc.status)
			gateCheckStatus(t, gateHTTP(h, gateTransitionPath, tc.token, transition), tc.status)
			assertStoredGate(t, s, gateA, store.PublicationGate{})
		})
	}
	h := New(s, Config{EnableCutover: true, PublicHost: gateManagementHost, AdminToken: gateAdminToken})
	for _, path := range []string{gateReadPath, gateTransitionPath} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+gateAdminToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		gateCheckStatus(t, w, http.StatusMethodNotAllowed)
	}
}

func assertStoredGate(t *testing.T, s *store.Store, host string, want store.PublicationGate) {
	t.Helper()
	got, err := s.GetPublicationGate(context.Background(), host)
	if err != nil || got != want {
		t.Fatalf("gate %q: got %+v, want %+v, err %v", host, got, want, err)
	}
}

func TestCutoverRefenceExactBlockedGroup(t *testing.T) {
	s, client, _ := gateFixture(t)
	config := Config{AdminToken: gateAdminToken, PublicHost: gateManagementHost, EnableCutover: true}
	h := New(s, config)
	const path = "/api/v1/cutover/gates/refence"
	initial := gateJSON(t, gateRequest([]testGate{{Host: gateA}, {Host: gateB}}, "blocked", "op", client))
	gateCheckStatus(t, gateHTTP(h, gateTransitionPath, gateAdminToken, initial), http.StatusNoContent)
	group := []testGate{{gateA, "blocked", 1, "op", client}, {gateB, "blocked", 1, "op", client}}
	refence := func(h http.Handler, token, management string, group []testGate, operation string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(gateJSON(t, gateRequest(group, "blocked", operation, client))))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("PortLoom-Expected-Management-Host", management)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		gateCheckStatus(t, w, want)
	}
	assertBoth := func(generation int64) {
		t.Helper()
		for _, host := range []string{gateA, gateB} {
			assertStoredGate(t, s, host, store.PublicationGate{Generation: generation, PublicationPolicy: store.PublicationPolicy{Phase: store.PublicationBlocked, OperationID: "op", ClientID: client}})
		}
	}
	config.EnableCutover = false
	refence(New(s, config), gateAdminToken, gateManagementHost, group, "op", http.StatusNotFound)
	refence(h, "wrong", gateManagementHost, group, "op", http.StatusUnauthorized)
	refence(h, gateAdminToken, "wrong.example.test", group, "op", http.StatusConflict)
	refence(h, gateAdminToken, gateManagementHost, group, "other-op", http.StatusBadRequest)
	stale := group[1]
	stale.Generation++
	refence(h, gateAdminToken, gateManagementHost, []testGate{group[0], stale}, "op", http.StatusConflict)
	assertBoth(1)
	refence(h, gateAdminToken, gateManagementHost, group, "op", http.StatusNoContent)
	refence(h, gateAdminToken, gateManagementHost, []testGate{group[1], group[0]}, "op", http.StatusNoContent)
	refence(h, gateAdminToken, gateManagementHost, []testGate{group[0]}, "op", http.StatusConflict)
	gateCheckStatus(t, gateHTTP(h, gateTransitionPath, gateAdminToken, gateJSON(t, gateRequest(group, "frozen", "op", client))), http.StatusConflict)
	assertBoth(2)
}

func TestCutoverGateAPIRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	s, client, _ := gateFixture(t)
	// The management host would be eligible based on routes and ownership alone.
	gateRouteFixture(t, s, client, gateManagementHost, "/business", domain.ProtocolHTTPS, 0)
	h := New(s, Config{EnableCutover: true, PublicHost: " CONSOLE.Example.Test. ", AdminToken: gateAdminToken})
	var many []string
	var manyGates []testGate
	for i := 0; i < 65; i++ {
		host := "host-" + strconv.Itoa(i) + ".example.test"
		many = append(many, host)
		manyGates = append(manyGates, testGate{Host: host})
	}
	for _, tc := range []struct{ name, body string }{
		{"malformed", `{"hosts":[`},
		{"empty group", `{"hosts":[]}`},
		{"null group", `{"hosts":null}`},
		{"null member", `{"hosts":[null]}`},
		{"too many hosts", string(gateJSON(t, map[string]any{"hosts": many}))},
		{"unknown field", `{"hosts":["a.example.test"],"extra":false}`},
		{"duplicate field", `{"hosts":["a.example.test"],"hosts":["b.example.test"]}`},
		{"trailing JSON", `{"hosts":["a.example.test"]}{}`},
		{"oversize", `{"hosts":["a.example.test"]}` + strings.Repeat(" ", maxRequestBody)},
		{"duplicate canonical host", `{"hosts":["a.example.test"," A.Example.Test. "]}`},
		{"configured management host", `{"hosts":[" CONSOLE.Example.Test. "]}`},
		{"wildcard", `{"hosts":["*.example.test"]}`},
		{"port", `{"hosts":["a.example.test:443"]}`},
		{"ip", `{"hosts":["127.0.0.1"]}`},
	} {
		t.Run("read/"+tc.name, func(t *testing.T) {
			res := gateHTTP(h, gateReadPath, gateAdminToken, []byte(tc.body))
			gateCheckStatus(t, res, http.StatusBadRequest)
			if res.Body.String() != "{\"error\":\"invalid_request\"}\n" {
				t.Fatalf("unexpected error: %s", res.Body.String())
			}
		})
	}
	request := func(expect ...testGate) string {
		return string(gateJSON(t, gateRequest(expect, "blocked", "op", client)))
	}
	valid := request(testGate{Host: gateA})
	for _, tc := range []struct{ name, body string }{
		{"management supplied by caller", `{"management_host":"not-console.example.test",` + valid[1:]},
		{"configured management host", request(testGate{Host: gateA}, testGate{Host: gateManagementHost})},
		{"invalid host", request(testGate{Host: gateA}, testGate{Host: "*.example.test"})},
		{"duplicate canonical host", request(testGate{Host: gateA}, testGate{Host: " A.Example.Test. "})},
		{"too many hosts", request(manyGates...)},
		{"empty group", request([]testGate{}...)},
		{"missing expectation generation", strings.Replace(valid, `"generation":0,`, "", 1)},
		{"missing open phase", strings.Replace(valid, `"phase":"",`, "", 1)},
		{"null operation ID", strings.Replace(valid, `"operation_id":""`, `"operation_id":null`, 1)},
		{"duplicate nested key", strings.Replace(valid, `"generation":0`, `"generation":1,"generation":0`, 1)},
		{"unknown nested key", strings.Replace(valid, `"generation":0`, `"generation":0,"other":0`, 1)},
		{"case alias", strings.Replace(valid, `"generation"`, `"Generation"`, 1)},
		{"decimal generation", strings.Replace(valid, `"generation":0`, `"generation":0.0`, 1)},
		{"negative generation", request(testGate{Host: gateA, Generation: -1})},
		{"overflow on increment", request(testGate{Host: gateA, Generation: 1<<63 - 1})},
		{"invalid expected phase", request(testGate{Host: gateA, Phase: "open"})},
		{"owned open", request(testGate{Host: gateA, OperationID: "op", ClientID: client})},
		{"ownerless blocked", request(testGate{Host: gateA, Generation: 1, Phase: "blocked"})},
		{"active zero generation", request(testGate{Host: gateA, Phase: "frozen", OperationID: "op", ClientID: client})},
		{"invalid next phase", strings.Replace(valid, `"blocked"`, `"open"`, 1)},
		{"missing next owner", strings.Replace(valid, `"operation_id":"op",`, "", 1)},
		{"blank next owner", strings.Replace(valid, `"operation_id":"op"`, `"operation_id":" "`, 1)},
		{"invalid UTF-8", strings.Replace(valid, `"op"`, "\"\xff\"", 1)},
		{"trailing JSON", valid + "{}"},
		{"oversize", valid + strings.Repeat(" ", maxRequestBody)},
	} {
		t.Run("transition/"+tc.name, func(t *testing.T) {
			res := gateHTTP(h, gateTransitionPath, gateAdminToken, []byte(tc.body))
			gateCheckStatus(t, res, http.StatusBadRequest)
			if res.Body.String() != "{\"error\":\"invalid_request\"}\n" {
				t.Fatalf("unexpected error: %s", res.Body.String())
			}
			assertStoredGate(t, s, gateA, store.PublicationGate{})
			assertStoredGate(t, s, gateManagementHost, store.PublicationGate{})
		})
	}
}

func TestCutoverGateAPIGroupConflictsLeaveEveryGateUnchanged(t *testing.T) {
	s, client, _ := gateFixture(t)
	other, _ := gateAgentFixture(t, s, "other-client")
	gateRouteFixture(t, s, other, gateB, "/foreign", domain.ProtocolHTTPS, 0)
	gateRouteFixture(t, s, client, "c-http.example.test", "", domain.ProtocolHTTP, 0)
	gateRouteFixture(t, s, client, "c-custom.example.test", "", domain.ProtocolHTTPS, 8443)
	h := New(s, Config{EnableCutover: true, PublicHost: gateManagementHost, AdminToken: gateAdminToken})
	transition := func(expect []testGate, phase, op, owner string, want int) {
		t.Helper()
		res := gateHTTP(h, gateTransitionPath, gateAdminToken, gateJSON(t, gateRequest(expect, phase, op, owner)))
		gateCheckStatus(t, res, want)
		if want == http.StatusConflict && res.Body.String() != "{\"error\":\"conflict\"}\n" {
			t.Fatalf("unexpected error: %s", res.Body.String())
		}
	}
	for _, host := range []string{gateB, "c-http.example.test", "c-custom.example.test", "c-missing.example.test"} {
		t.Run(host, func(t *testing.T) {
			transition([]testGate{{Host: gateA}, {Host: host}}, "frozen", "op", client, http.StatusConflict)
			assertStoredGate(t, s, gateA, store.PublicationGate{})
			assertStoredGate(t, s, host, store.PublicationGate{})
		})
	}
	transition([]testGate{{Host: gateA}}, "frozen", "op", client, http.StatusNoContent)
	current := testGate{Host: gateA, Phase: "frozen", Generation: 1, OperationID: "op", ClientID: client}
	transition([]testGate{current}, "blocked", "foreign-op", client, http.StatusConflict)
	transition([]testGate{current}, "blocked", "op", other, http.StatusConflict)
	wrong := current
	wrong.Phase = "blocked" // Generation and owner match, but the exact tuple does not.
	transition([]testGate{wrong}, "", "op", client, http.StatusConflict)
	assertStoredGate(t, s, gateA, store.PublicationGate{Generation: 1, PublicationPolicy: store.PublicationPolicy{Phase: store.PublicationFrozen, OperationID: "op", ClientID: client}})
}

func TestCutoverGateAPIStoreErrorsAreRedacted(t *testing.T) {
	s, client, _ := gateFixture(t)
	h := New(s, Config{EnableCutover: true, PublicHost: gateManagementHost, AdminToken: gateAdminToken})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{
		gateReadPath:       []byte(`{"hosts":["a.example.test"]}`),
		gateTransitionPath: gateJSON(t, gateRequest([]testGate{{Host: gateA}}, "blocked", "sensitive-op", client)),
	} {
		res := gateHTTP(h, path, gateAdminToken, body)
		gateCheckStatus(t, res, http.StatusInternalServerError)
		if res.Body.String() != "{\"error\":\"internal_error\"}\n" {
			t.Fatalf("unexpected error: %s", res.Body.String())
		}
	}
}
