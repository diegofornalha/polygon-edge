package network

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
)

func TestConnLimit_Inbound(t *testing.T) {
	// we should not receive more inbound connections if we are already connected to max peers
	defaultConfig := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 1
			c.NoDiscover = true
		},
	}

	servers, createErr := createServers(3, map[int]*CreateServerParams{
		0: defaultConfig,
		1: defaultConfig,
		2: defaultConfig,
	})
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	// One slot left, Server 0 can connect to Server 1
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Server 2 tries to connect to Server 0
	// but Server 0 is already connected to max peers
	smallTimeout := time.Second * 5
	if joinErr := JoinAndWait(servers[2], servers[0], smallTimeout, smallTimeout); joinErr == nil {
		t.Fatal("Peer join should've failed")
	}

	// Disconnect Server 1 from Server 0 so Server 0 will have free slots
	servers[0].Disconnect(servers[1].host.ID(), "bye")

	disconnectCtx, disconnectFn := context.WithTimeout(context.Background(), DefaultJoinTimeout)
	defer disconnectFn()

	if _, disconnectErr := WaitUntilPeerDisconnectsFrom(
		disconnectCtx,
		servers[0],
		servers[1].AddrInfo().ID,
	); disconnectErr != nil {
		t.Fatalf("Unable to disconnect from peer, %v", disconnectErr)
	}

	// Attempt a connection between Server 2 and Server 0 again
	if joinErr := JoinAndWait(servers[2], servers[0], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}
}

func TestConnLimit_Outbound(t *testing.T) {
	defaultConfig := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 1
			c.NoDiscover = true
		},
	}

	servers, createErr := createServers(3, map[int]*CreateServerParams{
		0: defaultConfig,
		1: defaultConfig,
		2: defaultConfig,
	})
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	// One slot left, Server 0 can connect to Server 1
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Attempt to connect Server 0 to Server 2, but it should fail since
	// Server 0 already has 1 peer (Server 1)
	smallTimeout := time.Second * 5
	if joinErr := JoinAndWait(servers[0], servers[2], smallTimeout, smallTimeout); joinErr == nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Disconnect Server 0 from Server 1
	servers[0].Disconnect(servers[1].host.ID(), "bye")

	disconnectCtx, disconnectFn := context.WithTimeout(context.Background(), DefaultJoinTimeout)
	defer disconnectFn()

	if _, disconnectErr := WaitUntilPeerDisconnectsFrom(
		disconnectCtx,
		servers[0],
		servers[1].AddrInfo().ID,
	); disconnectErr != nil {
		t.Fatalf("Unable to wait for disconnect from peer, %v", disconnectErr)
	}

	// Now Server 0 is trying to connect to Server 2 since there are slots left
	waitCtx, cancelWait := context.WithTimeout(context.Background(), DefaultJoinTimeout*2)
	defer cancelWait()

	_, err := WaitUntilPeerConnectsTo(waitCtx, servers[0], servers[2].host.ID())
	if err != nil {
		t.Fatalf("Unable to wait for peer connect, %v", err)
	}
}

func TestPeerEvent_EmitAndSubscribe(t *testing.T) {
	server, createErr := CreateServer(&CreateServerParams{ConfigCallback: func(c *Config) {
		c.NoDiscover = true
	}})
	if createErr != nil {
		t.Fatalf("Unable to create server, %v", createErr)
	}

	t.Cleanup(func() {
		assert.NoError(t, server.Close())
	})

	sub, err := server.Subscribe()
	assert.NoError(t, err)

	count := 10
	events := []PeerEventType{
		PeerConnected,
		PeerFailedToConnect,
		PeerDisconnected,
		PeerAlreadyConnected,
		PeerDialCompleted,
		PeerAddedToDialQueue,
	}

	getIDAndEventType := func(i int) (peer.ID, PeerEventType) {
		id := peer.ID(strconv.Itoa(i))
		event := events[i%len(events)]

		return id, event
	}

	t.Run("Serial event emit and read", func(t *testing.T) {
		for i := 0; i < count; i++ {
			id, event := getIDAndEventType(i)
			server.emitEvent(id, event)

			received := sub.Get()
			assert.Equal(t, &PeerEvent{id, event}, received)
		}
	})

	t.Run("Async event emit and read", func(t *testing.T) {
		for i := 0; i < count; i++ {
			id, event := getIDAndEventType(i)
			server.emitEvent(id, event)
		}
		for i := 0; i < count; i++ {
			received := sub.Get()
			id, event := getIDAndEventType(i)
			assert.Equal(t, &PeerEvent{id, event}, received)
		}
	})
}

func TestEncodingPeerAddr(t *testing.T) {
	_, pub, err := crypto.GenerateKeyPair(crypto.Secp256k1, 256)
	assert.NoError(t, err)

	id, err := peer.IDFromPublicKey(pub)
	assert.NoError(t, err)

	addr, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/90")
	assert.NoError(t, err)

	info := &peer.AddrInfo{
		ID:    id,
		Addrs: []multiaddr.Multiaddr{addr},
	}

	str := AddrInfoToString(info)
	info2, err := StringToAddrInfo(str)
	assert.NoError(t, err)
	assert.Equal(t, info, info2)
}

func TestAddrInfoToString(t *testing.T) {
	defaultPeerID := peer.ID("123")
	defaultPort := 5000

	// formatMultiaddr is a helper function for formatting an IP / port combination
	formatMultiaddr := func(ipVersion string, address string, port int) string {
		return fmt.Sprintf("/%s/%s/tcp/%d", ipVersion, address, port)
	}

	// formatExpectedOutput is a helper function for generating the expected output for AddrInfoToString
	formatExpectedOutput := func(input string, peerID string) string {
		return fmt.Sprintf("%s/p2p/%s", input, peerID)
	}

	testTable := []struct {
		name      string
		addresses []string
		expected  string
	}{
		{
			// A host address is explicitly specified
			"Valid dial address found",
			[]string{formatMultiaddr("ip4", "192.168.1.1", defaultPort)},
			formatExpectedOutput(
				formatMultiaddr("ip4", "192.168.1.1", defaultPort),
				defaultPeerID.String(),
			),
		},
		{
			// Multiple addresses that the host can listen on (0.0.0.0 special case)
			"Ignore IPv4 loopback address",
			[]string{
				formatMultiaddr("ip4", "127.0.0.1", defaultPort),
				formatMultiaddr("ip4", "192.168.1.1", defaultPort),
			},
			formatExpectedOutput(
				formatMultiaddr("ip4", "192.168.1.1", defaultPort),
				defaultPeerID.String(),
			),
		},
		{
			// Multiple addresses that the host can listen on (0.0.0.0 special case)
			"Ignore IPv6 loopback addresses",
			[]string{
				formatMultiaddr("ip6", "0:0:0:0:0:0:0:1", defaultPort),
				formatMultiaddr("ip6", "::1", defaultPort),
				formatMultiaddr("ip6", "2001:0db8:85a3:0000:0000:8a2e:0370:7334", defaultPort),
			},
			formatExpectedOutput(
				formatMultiaddr("ip6", "2001:db8:85a3::8a2e:370:7334", defaultPort),
				defaultPeerID.String(),
			),
		},
	}

	for _, testCase := range testTable {
		t.Run(testCase.name, func(t *testing.T) {
			// Construct the multiaddrs
			multiAddrs, constructErr := constructMultiAddrs(testCase.addresses)
			if constructErr != nil {
				t.Fatalf("Unable to construct multiaddrs, %v", constructErr)
			}

			dialAddress := AddrInfoToString(&peer.AddrInfo{
				ID:    defaultPeerID,
				Addrs: multiAddrs,
			})

			assert.Equal(t, testCase.expected, dialAddress)
		})
	}
}

func TestJoinWhenAlreadyConnected(t *testing.T) {
	// if we try to join an already connected node, the watcher
	// should finish as well
	servers, createErr := createServers(2, nil)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	// Server 0 should connect to Server 1
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Server 1 should attempt to connect to Server 0, but shouldn't error out
	// if since it's already connected
	if joinErr := JoinAndWait(servers[1], servers[0], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}
}

func TestNat(t *testing.T) {
	testIP := "192.0.2.1"
	testPort := 1500 // important to be less than 2000 because of other tests and more than 1024 because of OS security
	testMultiAddrString := fmt.Sprintf("/ip4/%s/tcp/%d", testIP, testPort)

	server, createErr := CreateServer(&CreateServerParams{ConfigCallback: func(c *Config) {
		c.NatAddr = net.ParseIP(testIP)
		c.Addr.Port = testPort
	}})
	if createErr != nil {
		t.Fatalf("Unable to create server, %v", createErr)
	}

	t.Cleanup(func() {
		assert.NoError(t, server.Close())
	})

	// There should be multiple listening addresses
	listenAddresses := server.host.Network().ListenAddresses()
	assert.Greater(t, len(listenAddresses), 1)

	// NAT IP should not be found in listen addresses
	for _, addr := range listenAddresses {
		assert.NotEqual(t, addr.String(), testMultiAddrString)
	}

	// There should only be a singly registered server address
	addrInfo := server.AddrInfo()
	registeredAddresses := addrInfo.Addrs

	assert.Equal(t, 1, len(registeredAddresses))

	// NAT IP should be found in registered server addresses
	found := false

	for _, addr := range registeredAddresses {
		if addr.String() == testMultiAddrString {
			found = true

			break
		}
	}

	assert.True(t, found)
}

// TestPeerReconnection checks whether the node is able to reconnect with bootnodes on losing all active connections
func TestPeerReconnection(t *testing.T) {
	bootnodeConfig1 := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 3
			c.NoDiscover = false
		},
	}
	bootnodeConfig2 := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 3
			c.NoDiscover = false
		},
	}
	// Create bootnodes
	bootnodes, createErr := createServers(2, map[int]*CreateServerParams{0: bootnodeConfig1, 1: bootnodeConfig2})
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, bootnodes)
	})

	defaultConfig1 := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 3
			c.NoDiscover = false
			c.Chain.Bootnodes = []string{
				AddrInfoToString(bootnodes[0].AddrInfo()),
				AddrInfoToString(bootnodes[1].AddrInfo()),
			}
		},
	}
	defaultConfig2 := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 3
			c.NoDiscover = false
			c.Chain.Bootnodes = []string{
				AddrInfoToString(bootnodes[0].AddrInfo()),
				AddrInfoToString(bootnodes[1].AddrInfo()),
			}
		},
	}

	servers, createErr := createServers(2, map[int]*CreateServerParams{0: defaultConfig1, 1: defaultConfig2})
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		for indx, server := range servers {
			if indx != 1 {
				// servers[1] is closed within the test
				assert.NoError(t, server.Close())
			}
		}
	})

	disconnectFromPeer := func(server *Server, peerID peer.ID) {
		server.Disconnect(peerID, "Bye")

		disconnectCtx, disconnectFn := context.WithTimeout(context.Background(), DefaultJoinTimeout)
		defer disconnectFn()

		if _, disconnectErr := WaitUntilPeerDisconnectsFrom(disconnectCtx, server, peerID); disconnectErr != nil {
			t.Fatalf("Unable to wait for disconnect from peer, %v", disconnectErr)
		}
	}

	closePeerServer := func(server *Server, peer *Server) {
		peerID := peer.AddrInfo().ID

		if closeErr := peer.Close(); closeErr != nil {
			t.Fatalf("Unable to close server, %v", closeErr)
		}

		disconnectCtx, disconnectFn := context.WithTimeout(context.Background(), DefaultJoinTimeout)
		defer disconnectFn()

		if _, disconnectErr := WaitUntilPeerDisconnectsFrom(disconnectCtx, server, peerID); disconnectErr != nil {
			t.Fatalf("Unable to wait for disconnect from peer, %v", disconnectErr)
		}
	}

	// connect with the first boot node
	if joinErr := JoinAndWait(servers[0], bootnodes[0], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// connect with the second boot node
	if joinErr := JoinAndWait(servers[0], bootnodes[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Connect with the second server
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// disconnect from the first boot node
	disconnectFromPeer(servers[0], bootnodes[0].AddrInfo().ID)

	// disconnect from the second boot node
	disconnectFromPeer(servers[0], bootnodes[1].AddrInfo().ID)

	// disconnect from the other server
	closePeerServer(servers[0], servers[1])

	waitCtx, cancelWait := context.WithTimeout(context.Background(), DefaultJoinTimeout*3)
	defer cancelWait()

	reconnected, err := WaitUntilPeerConnectsTo(waitCtx, servers[0], bootnodes[0].host.ID(), bootnodes[1].host.ID())
	if err != nil {
		t.Fatalf("Unable to wait for peer connect, %v", err)
	}

	assert.True(t, reconnected)
}

func TestReconnectionWithNewIP(t *testing.T) {
	natIP := "127.0.0.1"

	_, dir0 := GenerateTestLibp2pKey(t)
	_, dir1 := GenerateTestLibp2pKey(t)

	defaultConfig := func(c *Config) {
		c.NoDiscover = true
	}

	servers, createErr := createServers(3,
		map[int]*CreateServerParams{
			0: {
				ConfigCallback: func(c *Config) {
					defaultConfig(c)
					c.DataDir = dir0
				},
			},
			1: {
				ConfigCallback: func(c *Config) {
					defaultConfig(c)
					c.DataDir = dir1
				},
			},
			2: {
				ConfigCallback: func(c *Config) {
					defaultConfig(c)
					c.DataDir = dir1
					// same ID to but different IP from servers[1]
					c.NatAddr = net.ParseIP(natIP)
				},
			},
		},
	)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	// Server 0 should connect to Server 1
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Server 1 terminates, so Server 0 should disconnect from it
	peerID := servers[1].AddrInfo().ID

	if err := servers[1].host.Close(); err != nil {
		t.Fatalf("Unable to close peer server, %v", err)
	}

	disconnectCtx, disconnectFn := context.WithTimeout(context.Background(), DefaultJoinTimeout)
	defer disconnectFn()

	if _, disconnectErr := WaitUntilPeerDisconnectsFrom(disconnectCtx, servers[0], peerID); disconnectErr != nil {
		t.Fatalf("Unable to wait for disconnect from peer, %v", disconnectErr)
	}

	// servers[0] connects to servers[2]
	// Server 0 should connect to Server 2 (that has the NAT address set)
	if joinErr := JoinAndWait(servers[0], servers[2], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join servers, %v", joinErr)
	}

	// Wait until Server 2 also has a connection to Server 0 before asserting
	connectCtx, connectFn := context.WithTimeout(context.Background(), DefaultBufferTimeout)
	defer connectFn()

	if _, connectErr := WaitUntilPeerConnectsTo(
		connectCtx,
		servers[2],
		servers[0].AddrInfo().ID,
	); connectErr != nil {
		t.Fatalf("Unable to wait for connection between Server 2 and Server 0, %v", connectErr)
	}

	assert.Equal(t, int64(1), servers[0].numPeers())
	assert.Equal(t, int64(1), servers[2].numPeers())
}

func TestSelfConnection_WithBootNodes(t *testing.T) {
	// Create a temporary directory for storing the key file
	key, directoryName := GenerateTestLibp2pKey(t)
	peerID, err := peer.IDFromPrivateKey(key)
	assert.NoError(t, err)
	testMultiAddr := GenerateTestMultiAddr(t).String()
	peerAddressInfo, err := StringToAddrInfo(testMultiAddr)
	assert.NoError(t, err)

	testTable := []struct {
		name         string
		bootNodes    []string
		expectedList []*peer.AddrInfo
	}{

		{
			name:         "Should return an non empty bootnodes list",
			bootNodes:    []string{"/ip4/127.0.0.1/tcp/10001/p2p/" + peerID.Pretty(), testMultiAddr},
			expectedList: []*peer.AddrInfo{peerAddressInfo},
		},
	}

	for _, tt := range testTable {
		t.Run(tt.name, func(t *testing.T) {
			server, createErr := CreateServer(&CreateServerParams{ConfigCallback: func(c *Config) {
				c.NoDiscover = false
				c.DataDir = directoryName
				c.Chain.Bootnodes = tt.bootNodes
			}})
			if createErr != nil {
				t.Fatalf("Unable to create server, %v", createErr)
			}

			assert.Equal(t, tt.expectedList, server.discovery.bootnodes)
		})
	}
}

func TestRunDial(t *testing.T) {
	// setupServers returns server and list of peer's server
	setupServers := func(t *testing.T, maxPeers []uint64) []*Server {
		t.Helper()

		servers := make([]*Server, len(maxPeers))
		for idx := range servers {
			server, createErr := CreateServer(
				&CreateServerParams{
					ConfigCallback: func(c *Config) {
						c.MaxPeers = maxPeers[idx]
						c.NoDiscover = true
					},
				})
			if createErr != nil {
				t.Fatalf("Unable to create servers, %v", createErr)
			}

			servers[idx] = server
		}

		return servers
	}

	closeServers := func(servers ...*Server) {
		for _, s := range servers {
			assert.NoError(t, s.Close())
		}
	}

	t.Run("should connect to all peers", func(t *testing.T) {
		maxPeers := []uint64{2, 1, 1}
		servers := setupServers(t, maxPeers)
		srv, peers := servers[0], servers[1:]

		for _, p := range peers {
			if joinErr := JoinAndWait(srv, p, DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
				t.Fatalf("Unable to join peer, %v", joinErr)
			}
		}
		closeServers(servers...)
	})

	t.Run("should fail to connect to some peers due to reaching limit", func(t *testing.T) {
		maxPeers := []uint64{2, 1, 1, 1}
		servers := setupServers(t, maxPeers)
		srv, peers := servers[0], servers[1:]

		for idx, p := range peers {
			if uint64(idx) < maxPeers[0] {
				// Connection should be successful
				joinErr := JoinAndWait(srv, p, DefaultBufferTimeout, DefaultJoinTimeout)
				assert.NoError(t, joinErr)
			} else {
				// Connection should fail
				smallTimeout := time.Second * 5
				joinErr := JoinAndWait(srv, p, smallTimeout, smallTimeout)
				assert.Error(t, joinErr)
			}
		}
		closeServers(servers...)
	})

	t.Run("should try to connect after adding a peer to queue", func(t *testing.T) {
		maxPeers := []uint64{1, 0, 1}
		servers := setupServers(t, maxPeers)
		srv, peers := servers[0], servers[1:]

		// Server 1 can't connect to any peers, so this join should fail
		smallTimeout := time.Second * 5
		if joinErr := JoinAndWait(srv, peers[0], smallTimeout, smallTimeout); joinErr == nil {
			t.Fatalf("Shouldn't be able to join peer, %v", joinErr)
		}

		// Server 0 and Server 2 should connect
		if joinErr := JoinAndWait(srv, peers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
			t.Fatalf("Couldn't join peer, %v", joinErr)
		}

		closeServers(srv, peers[1])
	})
}

func TestMinimumBootNodeCount(t *testing.T) {
	tests := []struct {
		name       string
		bootNodes  []string
		shouldFail bool
	}{
		{
			name:       "Server config with empty bootnodes",
			bootNodes:  []string{},
			shouldFail: true,
		},
		{
			name:       "Server config with less than two bootnodes",
			bootNodes:  []string{GenerateTestMultiAddr(t).String()},
			shouldFail: true,
		},
		{
			name:       "Server config with more than two bootnodes",
			bootNodes:  []string{GenerateTestMultiAddr(t).String(), GenerateTestMultiAddr(t).String()},
			shouldFail: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, createErr := CreateServer(&CreateServerParams{
				ConfigCallback: func(c *Config) {
					c.Chain.Bootnodes = tt.bootNodes
				},
			})

			if tt.shouldFail {
				assert.Error(t, createErr)
			} else {
				assert.NoError(t, createErr)
			}
		})
	}
}
