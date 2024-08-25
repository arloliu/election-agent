package zc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_parseZoneString(t *testing.T) {
	require := require.New(t)
	zones, err := parseZoneString("default : c1, c2  ; test1  :c2")
	require.NotNil(zones)
	require.NoError(err)
	require.Equal([]string{"c1", "c2"}, zones["default"])
	require.Equal([]string{"c2"}, zones["test1"])

	zones, err = parseZoneString("test1:c2; ;test2:c1, c2")
	require.Nil(zones)
	require.EqualError(err, "contains empty key-value pair")

	zones, err = parseZoneString(" :c1,c2; test1:c2")
	require.Nil(zones)
	require.EqualError(err, "contains empty key")

	zones, err = parseZoneString("default:; test1:c2")
	require.Nil(zones)
	require.EqualError(err, "contains empty value")

	zones, err = parseZoneString("test1:c1,c2; test1:c2")
	require.Nil(zones)
	require.EqualError(err, "needs to have default zone")
}

func TestZC_V1(t *testing.T) {
	require := require.New(t)

	ctx := context.TODO()

	zcServer, err := NewServer(11900, "z1", "v1")
	require.NotNil(zcServer)
	require.NoError(err)

	go func() {
		_ = zcServer.Start()
	}()
	defer func() {
		_ = zcServer.Shutdown(ctx)
	}()

	resp, body := sendZCReq("http://localhost:11900", require)
	zcVer := resp.Header.Get("X-Api-Version")
	contentType := resp.Header.Get("Content-Type")

	require.Equal("v1", zcVer)
	require.Contains(contentType, "text/plain")
	require.Equal("z1", string(body))
}

func TestZC_V2(t *testing.T) {
	require := require.New(t)

	ctx := context.TODO()

	zcServer, err := NewServer(11900, "default:z2,z1;test:z1,z2", "v2")
	require.NotNil(zcServer)
	require.NoError(err)

	go func() {
		_ = zcServer.Start()
	}()
	defer func() {
		_ = zcServer.Shutdown(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// test root path
	resp, body := sendZCReq("http://localhost:11900", require)
	zcVer := resp.Header.Get("X-Api-Version")
	contentType := resp.Header.Get("Content-Type")
	require.Equal("v2", zcVer)
	require.Contains(contentType, "text/plain")
	require.Equal("z2", string(body))

	// test /v1
	resp, body = sendZCReq("http://localhost:11900/v1", require)
	zcVer = resp.Header.Get("X-Api-Version")
	contentType = resp.Header.Get("Content-Type")
	require.Equal("v2", zcVer)
	require.Contains(contentType, "text/plain")
	require.Equal("z2", string(body))

	// test v2 root path
	resp, body = sendZCReq("http://localhost:11900/v2", require)
	zcVer = resp.Header.Get("X-Api-Version")
	contentType = resp.Header.Get("Content-Type")
	require.Equal("v2", zcVer)
	require.Contains(contentType, "application/json")

	var zoneMap map[string][]string
	err = json.Unmarshal(body, &zoneMap)
	require.NoError(err)
	require.Equal([]string{"z2", "z1"}, zoneMap["default"])
	require.Equal([]string{"z1", "z2"}, zoneMap["test"])

	// test v2 specific groups
	var zones []string
	resp, body = sendZCReq("http://localhost:11900/v2/default", require)
	zcVer = resp.Header.Get("X-Api-Version")
	contentType = resp.Header.Get("Content-Type")
	require.Equal("v2", zcVer)
	require.Contains(contentType, "application/json")

	err = json.Unmarshal(body, &zones)
	require.NoError(err)
	require.Equal([]string{"z2", "z1"}, zones)

	resp, body = sendZCReq("http://localhost:11900/v2/test", require)
	zcVer = resp.Header.Get("X-Api-Version")
	contentType = resp.Header.Get("Content-Type")
	require.Equal("v2", zcVer)
	require.Contains(contentType, "application/json")

	err = json.Unmarshal(body, &zones)
	require.NoError(err)
	require.Equal([]string{"z1", "z2"}, zones)

	// test v2 non-exist path, expects to get default zone list
	resp, body = sendZCReq("http://localhost:11900/v2/nonexist", require)
	zcVer = resp.Header.Get("X-Api-Version")
	contentType = resp.Header.Get("Content-Type")
	require.Equal("v2", zcVer)
	require.Contains(contentType, "application/json")

	err = json.Unmarshal(body, &zones)
	require.NoError(err)
	require.Equal([]string{"z2", "z1"}, zones)
}

func sendZCReq(url string, require *require.Assertions) (*http.Response, []byte) {
	var req *http.Request
	var resp *http.Response
	var err error

	req, err = http.NewRequest(http.MethodGet, url, nil)
	require.NoError(err)

	req.Header.Add("Content-Type", "text/plain")
	req.Header.Add("User-Agent", "Election Agent")

	for i := 0; i < 10; i++ {
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	require.NoError(err)
	require.NotNil(resp)

	body, err := io.ReadAll(resp.Body)
	if resp.Body != nil {
		resp.Body.Close()
	}
	require.NoError(err)

	return resp, body
}
