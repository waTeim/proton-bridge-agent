package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	bridgepb "proton-bridge-sidecar/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	wrapperspb "google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	grpcConfigPath    = "/root/.config/protonmail/bridge-v3/grpcServerConfig.json"
	grpcConnectRetries = 30
	grpcConnectDelay   = 2 * time.Second
	grpcCallTimeout    = 30 * time.Second
)

type grpcServerConfig struct {
	FileSocketPath string `json:"fileSocketPath"`
	Token          string `json:"token"`
	Cert           string `json:"cert"`
	Port           int    `json:"port"`
}

type BridgeClient struct {
	mu           sync.RWMutex
	state        string // "idle", "pending", "connected", "error"
	stateMsg     string
	userID       string // bridge internal user ID (for LogoutUser)
	username     string // IMAP username (email address)
	imapPassword string // IMAP bridge password
	conn         *grpc.ClientConn
	grpcClient   bridgepb.BridgeClient
	callCtx      context.Context // background context with auth metadata
	watcherStop  chan struct{}
}

var globalBC *BridgeClient

func newBridgeClient() *BridgeClient {
	return &BridgeClient{state: "idle"}
}

func setBridgeClientGlobal(bc *BridgeClient) { globalBC = bc }
func getBridgeClient() *BridgeClient         { return globalBC }

// readGRPCConfig reads the bridge gRPC server config, retrying until the file appears.
// The bridge writes it at startup; the sidecar may start concurrently.
func readGRPCConfig() (*grpcServerConfig, error) {
	var lastErr error
	for i := 0; i < grpcConnectRetries; i++ {
		data, err := os.ReadFile(grpcConfigPath)
		if err == nil {
			var cfg grpcServerConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("parse gRPC config: %w", err)
			}
			return &cfg, nil
		}
		lastErr = err
		slog.Info("waiting for bridge gRPC config", "attempt", i+1, "path", grpcConfigPath)
		time.Sleep(grpcConnectDelay)
	}
	return nil, fmt.Errorf("gRPC config not found after %d attempts: %w", grpcConnectRetries, lastErr)
}

func buildConn(cfg *grpcServerConfig) (*grpc.ClientConn, bridgepb.BridgeClient, context.Context, error) {
	certPool := x509.NewCertPool()
	if cfg.Cert != "" {
		if !certPool.AppendCertsFromPEM([]byte(cfg.Cert)) {
			// fall back to skip-verify if cert can't be parsed
			certPool = nil
		}
	}

	var tlsCfg *tls.Config
	if certPool != nil {
		tlsCfg = &tls.Config{RootCAs: certPool}
	} else {
		tlsCfg = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed local cert
	}

	target := "unix://" + cfg.FileSocketPath
	//nolint:staticcheck // grpc.Dial is deprecated in v1.63+ but we target v1.62
	conn, err := grpc.Dial(target, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial gRPC: %w", err)
	}

	callCtx := metadata.AppendToOutgoingContext(context.Background(), "server-token", cfg.Token)
	return conn, bridgepb.NewBridgeClient(conn), callCtx, nil
}

// StartLogin begins an async login. Returns an error only if a login is already running.
func (bc *BridgeClient) StartLogin(username, password string) error {
	bc.mu.Lock()
	if bc.state == "pending" {
		bc.mu.Unlock()
		return fmt.Errorf("login already in progress")
	}
	bc.state = "pending"
	bc.stateMsg = ""
	bc.mu.Unlock()

	go bc.doLogin(username, password)
	return nil
}

func (bc *BridgeClient) doLogin(username, password string) {
	cfg, err := readGRPCConfig()
	if err != nil {
		bc.setError(err.Error())
		return
	}

	conn, client, callCtx, err := buildConn(cfg)
	if err != nil {
		bc.setError(err.Error())
		return
	}

	// Start event stream before Login so we don't miss the response event.
	streamCtx, streamCancel := context.WithCancel(callCtx)
	defer streamCancel()

	stream, err := client.RunEventStream(streamCtx, &bridgepb.EventStreamRequest{
		ClientPlatform: "sidecar",
	})
	if err != nil {
		conn.Close()
		bc.setError(fmt.Sprintf("start event stream: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
	_, err = client.Login(ctx, &bridgepb.LoginRequest{
		Username: username,
		Password: []byte(password),
	})
	cancel()
	if err != nil {
		conn.Close()
		bc.setError(fmt.Sprintf("login call: %v", err))
		return
	}

	userID, loginErr := waitForLoginEvent(stream)
	// Stop the event stream — we no longer need it.
	streamCancel()
	ctx, cancel = context.WithTimeout(callCtx, grpcCallTimeout)
	client.StopEventStream(ctx, &emptypb.Empty{}) //nolint:errcheck // best-effort
	cancel()

	if loginErr != nil {
		conn.Close()
		bc.setError(loginErr.Error())
		return
	}

	bc.finishLogin(conn, client, callCtx, userID)
}

func waitForLoginEvent(stream bridgepb.Bridge_RunEventStreamClient) (string, error) {
	for {
		evt, err := stream.Recv()
		if err != nil {
			return "", fmt.Errorf("event stream recv: %v", err)
		}

		loginEvt := evt.GetLogin()
		if loginEvt == nil {
			continue
		}

		switch e := loginEvt.Event.(type) {
		case *bridgepb.LoginEvent_Finished:
			return e.Finished.UserID, nil
		case *bridgepb.LoginEvent_AlreadyLoggedIn:
			return e.AlreadyLoggedIn.UserID, nil
		case *bridgepb.LoginEvent_Error:
			return "", fmt.Errorf("login error (%v): %s", e.Error.Type, e.Error.Message)
		case *bridgepb.LoginEvent_TfaRequested:
			return "", fmt.Errorf("2FA required — not supported by sidecar")
		case *bridgepb.LoginEvent_TwoPasswordRequested:
			return "", fmt.Errorf("two-password mode not supported by sidecar")
		case *bridgepb.LoginEvent_FidoRequested:
			return "", fmt.Errorf("FIDO required — not supported by sidecar")
		}
	}
}

func (bc *BridgeClient) finishLogin(conn *grpc.ClientConn, client bridgepb.BridgeClient, callCtx context.Context, userID string) {
	// Disable auto-updates — the binary bypass already prevents this, but belt-and-suspenders.
	ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
	_, _ = client.SetIsAutomaticUpdateOn(ctx, wrapperspb.Bool(false))
	cancel()

	ctx, cancel = context.WithTimeout(callCtx, grpcCallTimeout)
	resp, err := client.GetUserList(ctx, &emptypb.Empty{})
	cancel()
	if err != nil {
		conn.Close()
		bc.setError(fmt.Sprintf("get user list: %v", err))
		return
	}

	var imapUser, imapPass string
	for _, u := range resp.Users {
		if u.Id == userID || imapUser == "" {
			if len(u.Addresses) > 0 {
				imapUser = u.Addresses[0]
			} else {
				imapUser = u.Username
			}
			imapPass = string(u.Password)
			if u.Id == userID {
				break
			}
		}
	}

	if imapUser == "" {
		conn.Close()
		bc.setError("no user found after login")
		return
	}

	stopCh := make(chan struct{})

	bc.mu.Lock()
	bc.conn = conn
	bc.grpcClient = client
	bc.callCtx = callCtx
	bc.userID = userID
	bc.username = imapUser
	bc.imapPassword = imapPass
	bc.state = "connected"
	bc.stateMsg = ""
	bc.watcherStop = stopCh
	bc.mu.Unlock()

	slog.Info("bridge login succeeded", "username", imapUser)
	go watchIMAPInbox(stopCh, imapUser, imapPass)
}

func (bc *BridgeClient) setError(msg string) {
	bc.mu.Lock()
	bc.state = "error"
	bc.stateMsg = msg
	bc.mu.Unlock()
	slog.Error("bridge error", "error", msg)
}

// GetStatus returns the current state and optional message.
func (bc *BridgeClient) GetStatus() (string, string) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.state, bc.stateMsg
}

// GetUsername returns the IMAP username if connected, else "".
func (bc *BridgeClient) GetUsername() string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	if bc.state != "connected" {
		return ""
	}
	return bc.username
}

// Logout stops the IMAP watcher, calls LogoutUser on the bridge, and resets state.
func (bc *BridgeClient) Logout() {
	bc.mu.Lock()
	conn := bc.conn
	client := bc.grpcClient
	callCtx := bc.callCtx
	userID := bc.userID
	stopCh := bc.watcherStop

	bc.conn = nil
	bc.grpcClient = nil
	bc.callCtx = nil
	bc.userID = ""
	bc.username = ""
	bc.imapPassword = ""
	bc.state = "idle"
	bc.stateMsg = ""
	bc.watcherStop = nil
	bc.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}

	if client != nil && userID != "" && callCtx != nil {
		ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
		_, _ = client.LogoutUser(ctx, wrapperspb.String(userID))
		cancel()
	}

	if conn != nil {
		conn.Close()
	}

	slog.Info("bridge logout complete")
}
