package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"election-agent/internal/config"
	"election-agent/internal/logging"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHTTPService(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	cfg := config.Config{
		Env:          "test",
		LogLevel:     "debug",
		Name:         "test_election_agent",
		DefaultState: "active",
		KeyPrefix:    "test_agent",
		GRPC:         config.GRPCConfig{Enable: false},
		HTTP:         config.HTTPConfig{Enable: true, Port: 18080},
		Lease: config.LeaseConfig{
			CacheSize: 8192,
		},
		Zone: config.ZoneConfig{
			Enable: false,
			Name:   "zone1",
		},
	}

	server, err := startMockServer(ctx, &cfg)
	require.NoError(err)
	require.NotNil(server)
	defer server.Shutdown(ctx) //nolint:errcheck

	router := server.httpRouter

	// client1 campaign election1
	statusCode, resp, err := sendCampaign(router, "election1", "client1", 3000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)
	require.Equal(true, resp.Elected)
	require.Equal("client1", resp.Leader)

	// client1 campaign election2
	statusCode, resp, err = sendCampaign(router, "election2", "client1", 1000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)
	require.Equal(true, resp.Elected)
	require.Equal("client1", resp.Leader)

	// client2 campaign election1
	statusCode, resp, err = sendCampaign(router, "election1", "client2", 3000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)
	require.Equal(false, resp.Elected)
	require.Equal("", resp.Leader)

	// client1 campaign election1 again
	statusCode, resp, err = sendCampaign(router, "election1", "client1", 3000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)
	require.Equal(false, resp.Elected)
	require.Equal("", resp.Leader)

	// client1 extend elected term of election1
	statusCode, err = sendExtendElectedTerm(router, "election1", "client1", 3000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)

	// client1 resign for election1
	statusCode, err = sendResign(router, "election1", "client1")
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)

	// client1 campaign election1 again
	statusCode, resp, err = sendCampaign(router, "election1", "client1", 3000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)
	require.Equal(true, resp.Elected)
	require.Equal("client1", resp.Leader)

	// client2 campaign election1 again
	statusCode, resp, err = sendCampaign(router, "election1", "client2", 3000)
	require.NoError(err)
	require.Equal(http.StatusOK, statusCode)
	require.Equal(false, resp.Elected)
	require.Equal("", resp.Leader)
}

func sendCampaign(router *gin.Engine, election string, candidate string, term int32) (int, *CampaignResult, error) {
	req := CampaignRequest{Candidate: candidate, Term: term}
	body, err := json.Marshal(&req)
	if err != nil {
		logging.Errorw("Marshal CampaignRequest fail", "err", err)
		return http.StatusInternalServerError, nil, err
	}

	r, respBytes, err := sendRequest(router, "POST", fmt.Sprintf("/election/%s", election), bytes.NewBuffer(body))
	if err != nil {
		logging.Errorw("sendRequest fail", "err", err)
		return http.StatusInternalServerError, nil, err
	}

	if r.Code != http.StatusOK {
		return r.Code, nil, err
	}

	resp := CampaignResult{}
	err = json.Unmarshal(respBytes, &resp)
	if err != nil {
		logging.Errorw("Unmarshal CampaignResult fail", "respBytes", string(respBytes), "err", err)
		return r.Code, nil, err
	}

	return r.Code, &resp, nil
}

func sendExtendElectedTerm(router *gin.Engine, election string, leader string, term int32) (int, error) {
	req := ExtendElectedTermRequest{Leader: leader, Term: term}
	body, err := json.Marshal(&req)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	r, _, err := sendRequest(router, "PATCH", fmt.Sprintf("/election/%s", election), bytes.NewBuffer(body))
	if err != nil {
		return http.StatusInternalServerError, err
	}

	return r.Code, nil
}

func sendResign(router *gin.Engine, election string, leader string) (int, error) {
	req := ResignRequest{Leader: leader}
	body, err := json.Marshal(&req)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	r, _, err := sendRequest(router, "DELETE", fmt.Sprintf("/election/%s", election), bytes.NewBuffer(body))
	if err != nil {
		return http.StatusInternalServerError, err
	}

	return r.Code, nil
}

func sendRequest(router *gin.Engine, method, url string, body io.Reader) (*httptest.ResponseRecorder, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Add("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	resp, err := io.ReadAll(w.Body)
	if err != nil {
		return nil, nil, err
	}

	return w, resp, nil
}
