package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"election-agent/internal/config"
	"election-agent/internal/kube"
	"election-agent/internal/lease"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
)

type ElectionHTTPService struct {
	ctx        context.Context
	cfg        *config.Config
	leaseMgr   *lease.LeaseManager
	kubeClient kube.KubeClient
}

func newElectionHTTPService(
	ctx context.Context,
	cfg *config.Config,
	leaseMgr *lease.LeaseManager,
	kubeClient kube.KubeClient,
) *ElectionHTTPService {
	return &ElectionHTTPService{ctx: ctx, cfg: cfg, leaseMgr: leaseMgr, kubeClient: kubeClient}
}

func (s *ElectionHTTPService) MountHandlers(router *gin.Engine) {
	// election endpoints
	router.GET("/election/:kind/:name", s.getLeader)
	router.POST("/election/:kind/:name", s.campaign)
	router.PATCH("/election/:kind/:name", s.extendElectedTerm)
	router.DELETE("/election/:kind/:name", s.resign)

	// k8s related endpointes
	router.GET("/kube/:namespace/:deployment/pods", s.getPods)

	// the k8s liveness and readiness probe endpoints
	router.GET("/livez", s.livez)
	router.GET("/readyz", s.readyz)
}

type ErrorResponse struct {
	Message string `json:"candidate"`
}

type CampaignRequest struct {
	Candidate string `json:"candidate" binding:"required"`
	Term      int32  `json:"term" binding:"required,gte=1000"`
}

type CampaignResult struct {
	Elected bool   `json:"elected"`
	Leader  string `json:"leader"`
}

type ExtendElectedTermRequest struct {
	Leader        string `json:"leader" binding:"required"`
	Term          int32  `json:"term" binding:"required,gte=1000"`
	Retries       int32  `json:"retries" binding:"gte=0,lte=10"`
	RetryInterval int32  `json:"retry_interval" binding:"gte=0,lte=1000"`
}

type ResignRequest struct {
	Leader string `json:"leader" binding:"required"`
}

// GET handler for /election/:kind/:name, get the name of the leader elected in the election
func (s *ElectionHTTPService) getLeader(c *gin.Context) {
	election := c.Param("name")
	if election == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the election name is empty"})
		return
	}
	kind := c.Param("kind")

	leader, err := s.leaseMgr.GetLeaseHolder(s.ctx, election, kind)
	if err != nil {
		if lease.IsUnavailableError(err) {
			c.AbortWithStatusJSON(http.StatusPreconditionFailed, gin.H{"message": err.Error()})
			return
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("No leader found for the election %s", election)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"leader": leader})
}

// POST handler for /election/:kind/:name, election campaign
func (s *ElectionHTTPService) campaign(c *gin.Context) {
	election := c.Param("name")
	if election == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the election name is empty"})
		return
	}
	kind := c.Param("kind")

	var body CampaignRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	err := s.leaseMgr.GrantLease(s.ctx, election, kind, body.Candidate, time.Duration(body.Term)*time.Millisecond)
	if err != nil {
		if lease.IsUnavailableError(err) {
			c.AbortWithStatusJSON(http.StatusPreconditionFailed, gin.H{"message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"elected": false, "leader": ""})
		return
	}
	c.JSON(http.StatusOK, gin.H{"elected": true, "leader": body.Candidate})
}

// PATCH handler for /election/:kind/:name, extend the elected term of the election
func (s *ElectionHTTPService) extendElectedTerm(c *gin.Context) {
	election := c.Param("name")
	if election == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the election name is empty"})
		return
	}
	kind := c.Param("kind")

	var body ExtendElectedTermRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	retryInterval := time.Duration(body.RetryInterval) * time.Millisecond

	var err error
	for i := 0; i <= int(body.Retries); i++ {
		err = s.leaseMgr.ExtendLease(s.ctx, election, kind, body.Leader, time.Duration(body.Term)*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(retryInterval)
	}

	if err != nil {
		if lease.IsUnavailableError(err) {
			c.AbortWithStatusJSON(http.StatusPreconditionFailed, gin.H{"message": err.Error()})
		} else if lease.IsTakenError(err) {
			c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		} else if lease.IsNonexistError(err) {
			c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		}

		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.Status(http.StatusOK)
}

// DELETE handler for /election/:kind/:name, resign from the election
func (s *ElectionHTTPService) resign(c *gin.Context) {
	election := c.Param("name")
	if election == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the election name is empty"})
		return
	}
	kind := c.Param("kind")

	var body ResignRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	err := s.leaseMgr.RevokeLease(s.ctx, election, kind, body.Leader)
	if err != nil {
		if lease.IsUnavailableError(err) {
			c.AbortWithStatusJSON(http.StatusPreconditionFailed, gin.H{"message": err.Error()})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	}

	c.Status(http.StatusOK)
}

// GET handler for /kube/:namespace/:deployment/pods, get a list of pods info by namespace and deployment name
func (s *ElectionHTTPService) getPods(c *gin.Context) {
	if !s.cfg.Kube.Enable || s.kubeClient == nil {
		c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{"message": "the k8s feature is not enabled"})
		return
	}

	namespace := c.Param("namespace")
	deployment := c.Param("deployment")
	if namespace == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the namespace name is empty"})
		return
	}
	if deployment == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "the namespace name is empty"})
		return
	}

	pods, err := s.kubeClient.GetPods(namespace, deployment)
	if err != nil {
		statusCode := http.StatusBadRequest
		if pods != nil && len(pods.Items) == 0 {
			statusCode = http.StatusNotFound
		}
		c.AbortWithStatusJSON(statusCode, gin.H{"message": err.Error()})
		return
	}

	codec := protojson.MarshalOptions{EmitUnpopulated: true}
	data, err := codec.Marshal(pods)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.Data(http.StatusOK, "application/json; charset=utf-8", data)
}

func (s *ElectionHTTPService) livez(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *ElectionHTTPService) readyz(c *gin.Context) {
	if s.leaseMgr.Ready() {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	c.JSON(http.StatusInternalServerError, gin.H{"status": "ok"})
}
