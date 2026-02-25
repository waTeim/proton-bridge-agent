package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
	grpcConfigPath     = "/root/.config/protonmail/bridge-v3/grpcServerConfig.json"
	grpcConnectRetries = 30
	grpcConnectDelay   = 2 * time.Second
	grpcCallTimeout    = 30 * time.Second
	loginEventTimeout  = 120 * time.Second
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

// connectAndReady waits for the bridge gRPC config file to appear, dials the Unix socket,
// and calls GuiReady — retrying the whole sequence until all three succeed. The config file
// is written slightly before the socket is connectable, so a single pass is not sufficient.
func connectAndReady() (*grpc.ClientConn, bridgepb.BridgeClient, context.Context, error) {
	var lastErr error
	for i := 0; i < grpcConnectRetries; i++ {
		data, err := os.ReadFile(grpcConfigPath)
		if err != nil {
			lastErr = err
			slog.Info("waiting for bridge gRPC config", "attempt", i+1, "path", grpcConfigPath)
			time.Sleep(grpcConnectDelay)
			continue
		}

		var cfg grpcServerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, nil, nil, fmt.Errorf("parse gRPC config: %w", err)
		}

		conn, client, callCtx, err := buildConn(&cfg)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("build conn: %w", err)
		}

		ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
		_, err = client.GuiReady(ctx, &emptypb.Empty{})
		cancel()
		if err != nil {
			conn.Close()
			lastErr = err
			slog.Info("waiting for bridge socket", "attempt", i+1)
			time.Sleep(grpcConnectDelay)
			continue
		}

		return conn, client, callCtx, nil
	}
	return nil, nil, nil, fmt.Errorf("bridge not ready after %d attempts: %w", grpcConnectRetries, lastErr)
}

func buildConn(cfg *grpcServerConfig) (*grpc.ClientConn, bridgepb.BridgeClient, context.Context, error) {
	certPool := x509.NewCertPool()
	if cfg.Cert != "" {
		if !certPool.AppendCertsFromPEM([]byte(cfg.Cert)) {
			// fall back to skip-verify if cert can't be parsed
			certPool = nil
		}
	}

	// ServerName must be set for TLS over a Unix socket — there is no hostname
	// to derive it from. The bridge's self-signed cert is issued to "127.0.0.1".
	var tlsCfg *tls.Config
	if certPool != nil {
		tlsCfg = &tls.Config{RootCAs: certPool, ServerName: "127.0.0.1"}
	} else {
		tlsCfg = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed local cert
	}

	target := "unix://" + cfg.FileSocketPath
	//nolint:staticcheck // grpc.Dial is deprecated in v1.63+ but we target v1.64
	conn, err := grpc.Dial(target, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial gRPC: %w", err)
	}

	callCtx := metadata.AppendToOutgoingContext(context.Background(), "server-token", cfg.Token)
	return conn, bridgepb.NewBridgeClient(conn), callCtx, nil
}

// TryAutoLogin checks whether the bridge already has a logged-in user (e.g. after a pod
// restart with intact PVC) and, if so, transitions directly to connected without needing
// credentials to be supplied via the API.
//
// On startup, vault users begin in LOCKED state while the bridge reconnects to Proton's
// servers. We subscribe to the event stream and wait for the user to reach CONNECTED
// (signalled by UserChangedEvent or AllUsersLoadedEvent) before starting the IMAP watcher.
func (bc *BridgeClient) TryAutoLogin() {
	bc.mu.Lock()
	bc.state = "pending"
	bc.mu.Unlock()

	conn, client, callCtx, err := connectAndReady()
	if err != nil {
		bc.mu.Lock()
		bc.state = "idle"
		bc.mu.Unlock()
		slog.Info("auto-login: bridge not ready", "error", err)
		return
	}

	// Open event stream before GetUserList so we don't miss UserChangedEvents
	// emitted while the bridge is completing its startup reconnection.
	streamCtx, streamCancel := context.WithCancel(callCtx)
	defer streamCancel()

	stream, err := client.RunEventStream(streamCtx, &bridgepb.EventStreamRequest{
		ClientPlatform: "sidecar",
	})
	if err != nil {
		conn.Close()
		bc.mu.Lock()
		bc.state = "idle"
		bc.mu.Unlock()
		slog.Info("auto-login: could not start event stream", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
	resp, err := client.GetUserList(ctx, &emptypb.Empty{})
	cancel()
	if err != nil || len(resp.Users) == 0 {
		stopEventStream(client, callCtx)
		conn.Close()
		bc.mu.Lock()
		bc.state = "idle"
		bc.mu.Unlock()
		slog.Info("auto-login: no existing session, waiting for credentials")
		return
	}

	userID := resp.Users[0].Id
	userState := resp.Users[0].State

	if userState != bridgepb.UserState_CONNECTED {
		slog.Info("auto-login: user not yet connected, waiting", "state", userState)
		if err := waitForUserConnected(callCtx, client, stream, userID); err != nil {
			stopEventStream(client, callCtx)
			conn.Close()
			bc.mu.Lock()
			bc.state = "idle"
			bc.mu.Unlock()
			slog.Info("auto-login: user did not reach connected state", "error", err)
			return
		}
	}

	stopEventStream(client, callCtx)
	// defer streamCancel() runs after finishLogin returns — RunEventStream already exited.

	slog.Info("auto-login: existing session found, restoring")
	bc.finishLogin(conn, client, callCtx, userID)
}

// waitForUserConnected listens on the event stream for UserChangedEvent or AllUsersLoadedEvent
// and polls GetUser until the user reaches CONNECTED state, or until a 60s timeout.
func waitForUserConnected(callCtx context.Context, client bridgepb.BridgeClient, stream bridgepb.Bridge_RunEventStreamClient, userID string) error {
	timeoutCtx, cancel := context.WithTimeout(callCtx, 60*time.Second)
	defer cancel()

	recvCh := make(chan struct {
		evt *bridgepb.StreamEvent
		err error
	}, 1)

	go func() {
		for {
			evt, err := stream.Recv()
			recvCh <- struct {
				evt *bridgepb.StreamEvent
				err error
			}{evt, err}
			if err != nil {
				return
			}
		}
	}()

	checkUser := func() (bool, error) {
		ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
		user, err := client.GetUser(ctx, wrapperspb.String(userID))
		cancel()
		if err != nil {
			return false, fmt.Errorf("get user: %w", err)
		}
		slog.Info("auto-login: user state", "state", user.State)
		return user.State == bridgepb.UserState_CONNECTED, nil
	}

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timed out waiting for user to connect (60s)")

		case r := <-recvCh:
			if r.err != nil {
				return fmt.Errorf("event stream: %v", r.err)
			}

			var relevant bool
			if userEvt := r.evt.GetUser(); userEvt != nil {
				if ch := userEvt.GetUserChanged(); ch != nil && ch.UserID == userID {
					relevant = true
				}
			}
			if appEvt := r.evt.GetApp(); appEvt != nil && appEvt.GetAllUsersLoaded() != nil {
				relevant = true
			}

			if !relevant {
				continue
			}

			connected, err := checkUser()
			if err != nil {
				return err
			}
			if connected {
				return nil
			}
		}
	}
}

// stopEventStream calls StopEventStream on the bridge. Must be called before cancelling
// the stream context to avoid triggering s.quit() in the bridge's RunEventStream handler.
func stopEventStream(client bridgepb.BridgeClient, callCtx context.Context) {
	ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
	_, _ = client.StopEventStream(ctx, &emptypb.Empty{})
	cancel()
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
	// connectAndReady waits for the bridge socket and calls GuiReady.
	conn, client, callCtx, err := connectAndReady()
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

	// The bridge's Login handler calls base64.StdEncoding.Decode on the password bytes,
	// so we must base64-encode the plaintext password before sending it.
	encodedPassword := base64.StdEncoding.EncodeToString([]byte(password))

	ctx, cancel := context.WithTimeout(callCtx, grpcCallTimeout)
	_, err = client.Login(ctx, &bridgepb.LoginRequest{
		Username: username,
		Password: []byte(encodedPassword),
	})
	cancel()
	if err != nil {
		conn.Close()
		bc.setError(fmt.Sprintf("login call: %v", err))
		return
	}

	// Wait for the bridge to emit a login result event; enforce a hard timeout so we
	// never hang indefinitely (e.g. on unexpected HV/FIDO events).
	loginCtx, loginCancel := context.WithTimeout(streamCtx, loginEventTimeout)
	defer loginCancel()

	userID, loginErr := waitForLoginEvent(loginCtx, stream)

	// Stop the event stream via StopEventStream RPC *before* cancelling the
	// stream context. RunEventStream selects on three cases:
	//   1. eventStreamDoneCh  → returns nil (safe, gRPC server stays up)
	//   2. server.Context().Done() → calls s.quit() (KILLS the gRPC server!)
	//   3. event to forward
	// Calling streamCancel() first fires case 2 and tears down the socket.
	// StopEventStream sends to eventStreamDoneCh (case 1) and blocks until
	// RunEventStream reads it, so by the time it returns RunEventStream has
	// already taken the safe path. The deferred streamCancel() then has no
	// live RunEventStream goroutine to affect.
	stopEventStream(client, callCtx)

	if loginErr != nil {
		conn.Close()
		bc.setError(loginErr.Error())
		return
	}

	bc.finishLogin(conn, client, callCtx, userID)
}

func waitForLoginEvent(ctx context.Context, stream bridgepb.Bridge_RunEventStreamClient) (string, error) {
	recvCh := make(chan struct {
		evt *bridgepb.StreamEvent
		err error
	}, 1)

	go func() {
		for {
			evt, err := stream.Recv()
			recvCh <- struct {
				evt *bridgepb.StreamEvent
				err error
			}{evt, err}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for login event (%s)", loginEventTimeout)

		case r := <-recvCh:
			if r.err != nil {
				return "", fmt.Errorf("event stream recv: %v", r.err)
			}

			loginEvt := r.evt.GetLogin()
			if loginEvt == nil {
				continue
			}

			slog.Debug("login event received", "type", fmt.Sprintf("%T", loginEvt.Event))

			switch e := loginEvt.Event.(type) {
			case *bridgepb.LoginEvent_Finished:
				return e.Finished.UserID, nil
			case *bridgepb.LoginEvent_AlreadyLoggedIn:
				return e.AlreadyLoggedIn.UserID, nil
			case *bridgepb.LoginEvent_Error:
				return "", fmt.Errorf("login error (%v): %s", e.Error.Type, e.Error.Message)
			case *bridgepb.LoginEvent_TfaRequested:
				return "", fmt.Errorf("2FA (TOTP) required — not supported by sidecar")
			case *bridgepb.LoginEvent_TfaOrFidoRequested:
				return "", fmt.Errorf("2FA or FIDO required — not supported by sidecar")
			case *bridgepb.LoginEvent_TwoPasswordRequested:
				return "", fmt.Errorf("two-password mode required — not supported by sidecar")
			case *bridgepb.LoginEvent_FidoRequested:
				return "", fmt.Errorf("FIDO required — not supported by sidecar")
			case *bridgepb.LoginEvent_HvRequested:
				return "", fmt.Errorf("human verification required (URL: %s) — complete in browser then retry", e.HvRequested.HvUrl)
			}
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
