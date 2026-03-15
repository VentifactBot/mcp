package main

// mockTransport implements Transport for testing.
type mockTransport struct {
	sendFunc   func(req jsonrpcRequest) (jsonrpcResponse, error)
	streamFunc func(req jsonrpcRequest, onEvent func(streamEvent)) (jsonrpcResponse, error)
	notifyFunc func(notif jsonrpcNotification) error
	closeFunc  func() error
}

func (m *mockTransport) Send(req jsonrpcRequest) (jsonrpcResponse, error) {
	if m.sendFunc != nil {
		return m.sendFunc(req)
	}
	return jsonrpcResponse{}, nil
}

func (m *mockTransport) SendStreaming(req jsonrpcRequest, onEvent func(streamEvent)) (jsonrpcResponse, error) {
	if m.streamFunc != nil {
		return m.streamFunc(req, onEvent)
	}
	return m.Send(req)
}

func (m *mockTransport) Notify(notif jsonrpcNotification) error {
	if m.notifyFunc != nil {
		return m.notifyFunc(notif)
	}
	return nil
}

func (m *mockTransport) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// setupTestConfig sets up an isolated config dir for tests.
func setupTestConfigDir(t interface{ TempDir() string; Cleanup(func()) }) string {
	dir := t.TempDir()
	testConfigDir = dir
	t.Cleanup(func() { testConfigDir = "" })
	ensureConfigDirs()
	return dir
}
