package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mobile-egress/internal/capacity"
	"mobile-egress/relay/internal/enrollment"
)

type Service struct {
	metrics       trafficMetrics
	metricsCancel context.CancelFunc
	metricsDone   chan struct{}
	workers       sync.WaitGroup
	shutdownOnce  sync.Once
	shutdownError error
	store         *store
	caCert        *x509.Certificate
	caCertPEM     []byte
	caKey         crypto.Signer
	serverCert    tls.Certificate
	clientRoots   *x509.CertPool

	mu                   sync.RWMutex
	agentConnected       bool
	connectedClients     int
	activeStreams        int
	closed               bool
	agent                *session
	sessions             map[string]*session
	pendingSessions      map[string]struct{}
	pendingOpens         map[string]*pendingOpen
	resolverWorkers      int
	agentPending         chan struct{}
	streams              map[string]*stream
	closedStreams        map[string]closedStreamTombstone
	maxResolverWorkers   int
	openingTimeout       time.Duration
	idleTimeout          time.Duration
	sweepInterval        time.Duration
	janitorOnce          sync.Once
	stopJanitor          chan struct{}
	lookupNetIP          lookupNetIPFunc
	beforeDataAdmission  func()
	agentToClientsBudget *outboundDataBudget
}

type healthResponse struct {
	Readiness       bool             `json:"readiness"`
	AgentConnected  bool             `json:"agentConnected"`
	AgentPaired     *bool            `json:"agentPaired,omitempty"`
	ConnectedClient int              `json:"connectedClients"`
	ActiveStreams   int              `json:"activeStreams"`
	TotalStreams    int64            `json:"totalStreams"`
	ByteCount       int64            `json:"byteCount"`
	ErrorCounts     map[string]int64 `json:"errorCounts"`
}

func Open(stateDir string) (*Service, error) {
	stateDir = filepath.Clean(stateDir)
	info, err := os.Stat(stateDir)
	if err != nil {
		return nil, fmt.Errorf("open initialized state: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("initialized state path is not a directory")
	}

	caCertPEM, err := os.ReadFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	caBlock, rest := pem.Decode(caCertPEM)
	if caBlock == nil || caBlock.Type != "CERTIFICATE" || len(rest) != 0 {
		return nil, errors.New("invalid CA certificate state")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !caCert.IsCA || !caCert.BasicConstraintsValid {
		return nil, errors.New("invalid CA certificate state")
	}

	caKeyPEM, err := os.ReadFile(filepath.Join(stateDir, caKeyFilename))
	if err != nil {
		return nil, fmt.Errorf("read CA private key: %w", err)
	}
	caKeyBlock, rest := pem.Decode(caKeyPEM)
	if caKeyBlock == nil || caKeyBlock.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, errors.New("invalid CA private key state")
	}
	parsedCAKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, errors.New("invalid CA private key state")
	}
	caKey, ok := parsedCAKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("unsupported CA private key state")
	}
	if !caKey.PublicKey.Equal(caCert.PublicKey) {
		return nil, errors.New("CA certificate and private key do not match")
	}

	serverCertPEM, err := os.ReadFile(filepath.Join(stateDir, relayCertFilename))
	if err != nil {
		return nil, fmt.Errorf("read relay certificate: %w", err)
	}
	serverKeyPEM, err := os.ReadFile(filepath.Join(stateDir, relayKeyFilename))
	if err != nil {
		return nil, fmt.Errorf("read relay private key: %w", err)
	}
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		return nil, errors.New("invalid relay certificate state")
	}
	serverLeaf, err := x509.ParseCertificate(serverCert.Certificate[0])
	if err != nil {
		return nil, errors.New("invalid relay certificate state")
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := serverLeaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, errors.New("relay certificate does not verify against initialized CA")
	}
	serverCert.Leaf = serverLeaf

	state, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		return nil, err
	}
	if err := state.validSchema(context.Background()); err != nil {
		state.Close()
		return nil, fmt.Errorf("validate SQLite state: %w", err)
	}

	snapshot, err := state.metrics(context.Background())
	if err != nil {
		state.Close()
		return nil, err
	}
	metricsContext, metricsCancel := context.WithCancel(context.Background())
	service := &Service{
		metrics: trafficMetrics{totals: snapshot}, metricsCancel: metricsCancel, metricsDone: make(chan struct{}),
		store: state, caCert: caCert, caCertPEM: caCertPEM, caKey: caKey,
		serverCert: serverCert, clientRoots: roots,
		sessions: make(map[string]*session), pendingSessions: make(map[string]struct{}), streams: make(map[string]*stream),
		closedStreams:        make(map[string]closedStreamTombstone),
		agentToClientsBudget: newOutboundDataBudget(capacity.DataFramesPerLane, capacity.DataBytesPerLane),
		maxResolverWorkers:   capacity.ResolverWorkers,
		openingTimeout:       30 * time.Second, idleTimeout: 5 * time.Minute,
		sweepInterval: time.Second, stopJanitor: make(chan struct{}),
		lookupNetIP: defaultLookupNetIP,
	}
	go service.runMetricsWriter(metricsContext)
	return service, nil
}

func (service *Service) Close() error {
	service.shutdownOnce.Do(func() {
		service.mu.Lock()
		service.closed = true
		close(service.stopJanitor)
		sessions := make([]*session, 0, len(service.sessions))
		for _, activeSession := range service.sessions {
			sessions = append(sessions, activeSession)
		}
		service.mu.Unlock()
		for _, activeSession := range sessions {
			activeSession.close("session_closed")
		}
		service.workers.Wait()
		service.metricsCancel()
		<-service.metricsDone
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		flushError := service.metrics.flush(ctx, service.store)
		service.shutdownError = errors.Join(flushError, service.store.Close())
	})
	return service.shutdownError
}

func (service *Service) TLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{service.serverCert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    service.clientRoots,
	}
}

func (service *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.handleHealth)
	mux.HandleFunc("POST /v1/enroll", service.handleEnroll)
	mux.HandleFunc("POST /v1/clients", service.handleProvisionClient)
	mux.HandleFunc("POST /v1/pairing-codes", service.handlePairing)
	mux.HandleFunc("POST /v1/revoke", service.handleRevoke)
	mux.HandleFunc("POST /v1/endpoint-migrations", service.handleIssueEndpointMigration)
	mux.HandleFunc("POST /v1/endpoint-migrations/consume", service.handleConsumeEndpointMigration)
	mux.HandleFunc("GET /v1/session", service.handleSession)
	return mux
}

func (service *Service) handleHealth(writer http.ResponseWriter, request *http.Request) {
	_, err := service.store.metrics(request.Context())
	metrics, _ := service.metrics.snapshot()
	service.mu.RLock()
	response := healthResponse{
		Readiness: !service.closed && err == nil, AgentConnected: service.agentConnected,
		ConnectedClient: service.connectedClients, ActiveStreams: service.activeStreams,
		TotalStreams: metrics.TotalStreams, ByteCount: metrics.ByteCount, ErrorCounts: metrics.ErrorCounts,
	}
	service.mu.RUnlock()
	// Enrollment history is available only to the authenticated active Owner.
	// Older callers and unauthenticated readiness probes retain their schema.
	if request.Header.Get("X-Mobile-Egress-Health-Agent-Pairing") == "1" {
		if _, role, status := service.authenticateRequest(request); status == 0 && role == enrollment.RoleOwner {
			if count, countErr := service.store.activeIdentityCount(request.Context(), enrollment.RoleAgent); countErr == nil {
				paired := count > 0
				response.AgentPaired = &paired
			}
		}
	}
	writer.Header().Set("Content-Type", "application/json")
	if err != nil {
		response.ErrorCounts = map[string]int64{}
		writer.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(writer).Encode(response)
}

func (service *Service) Serve(server *http.Server) error {
	server.Handler = service.Handler()
	server.TLSConfig = service.TLSConfig()
	return server.ListenAndServeTLS("", "")
}
