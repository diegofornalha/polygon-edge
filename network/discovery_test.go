package network

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func discoveryConfig(c *Config) {
	// we limit maxPeers=1 to limit the number of connections
	// since we only want to test discovery
	c.MaxPeers = 2
}

func TestDiscovery_ConnectedPopulatesRoutingTable(t *testing.T) {
	// when two nodes connect, they populate their kademlia routing tables
	servers, createErr := createServers(2, nil)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout)
	if joinErr != nil {
		t.Fatalf("Unable to join peers, %v", joinErr)
	}

	assert.Equal(t, servers[0].discovery.routingTable.Size(), 1)
	assert.Equal(t, servers[1].discovery.routingTable.Size(), 1)
}

func TestDiscovery_ProtocolFindPeers(t *testing.T) {
	servers, createErr := createServers(2, nil)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout)
	if joinErr != nil {
		t.Fatalf("Unable to join peers, %v", joinErr)
	}

	// find peers should not include our identity
	resp, err := servers[0].discovery.findPeersCall(servers[1].AddrInfo().ID)
	assert.NoError(t, err)
	assert.Empty(t, resp)
}

func TestRoutingTable_Connected(t *testing.T) {
	defaultConfig := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 1
		},
	}
	paramsMap := map[int]*CreateServerParams{
		0: defaultConfig,
		1: defaultConfig,
	}

	servers, createErr := createServers(2, paramsMap)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join peers, %v", joinErr)
	}

	// make sure each routing table has peer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	t.Cleanup(func() {
		cancel()
	})

	if _, err := WaitUntilRoutingTableToBeFilled(ctx, servers[0], 1); err != nil {
		t.Fatalf("server 0 should add a peer to routing table but didn't, peer=%s", servers[1].host.ID())
	}

	if _, err := WaitUntilRoutingTableToBeFilled(ctx, servers[1], 1); err != nil {
		t.Fatalf("server 1 should add a peer to routing table but didn't, peer=%s", servers[0].host.ID())
	}

	assert.Contains(t, servers[0].discovery.routingTable.ListPeers(), servers[1].AddrInfo().ID)
	assert.Contains(t, servers[1].discovery.routingTable.ListPeers(), servers[0].AddrInfo().ID)
}

func TestRoutingTable_Disconnected(t *testing.T) {
	defaultConfig := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 1
		},
	}
	paramsMap := map[int]*CreateServerParams{
		0: defaultConfig,
		1: defaultConfig,
	}

	servers, createErr := createServers(3, paramsMap)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	// connect to peer and make sure peer is in routing table
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join peers, %v", joinErr)
	}

	// make sure each routing table has peer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	t.Cleanup(func() {
		cancel()
	})

	if _, err := WaitUntilRoutingTableToBeFilled(ctx, servers[0], 1); err != nil {
		t.Fatalf("server 0 should add a peer to routing table but didn't, peer=%s", servers[1].host.ID())
	}

	if _, err := WaitUntilRoutingTableToBeFilled(ctx, servers[1], 1); err != nil {
		t.Fatalf("server 1 should add a peer to routing table but didn't, peer=%s", servers[0].host.ID())
	}

	// disconnect
	if leaveErr := LeaveAndWait(servers[1], servers[0], DefaultLeaveTimeout); leaveErr != nil {
		t.Fatalf("Unable to disconnect from server[0], err=%v", leaveErr)
	}

	// make sure each routing table remove a peer
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)

	t.Cleanup(func() {
		cancel2()
	})

	if _, err := WaitUntilRoutingTableToBeFilled(ctx2, servers[0], 0); err != nil {
		t.Fatalf("server 0 should remove a peer from routing table but didn't, peer=%s", servers[1].host.ID())
	}

	if _, err := WaitUntilRoutingTableToBeFilled(ctx2, servers[1], 0); err != nil {
		t.Fatalf("server 1 should remove a peer from routing table but didn't, peer=%s", servers[0].host.ID())
	}
}

func TestRoutingTable_ConnectionFailure(t *testing.T) {
	defaultConfig := &CreateServerParams{
		ConfigCallback: func(c *Config) {
			c.MaxPeers = 1
		},
	}
	paramsMap := map[int]*CreateServerParams{
		0: defaultConfig,
		1: defaultConfig,
	}

	servers, createErr := createServers(3, paramsMap)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		// close only servers[0] because servers[1] has closed already
		closeTestServers(t, servers[:1])
	})

	// close before dialing
	if err := servers[1].Close(); err != nil {
		t.Fatal(err)
	}

	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr == nil {
		t.Fatalf("should failed to connect to server[1], but connected")
	}

	// routing tables should be empty
	for _, srv := range servers {
		assert.Equal(t, 0, srv.discovery.routingTable.Size())
	}
}

func TestDiscovery_FullNetwork(t *testing.T) {
	defaultConfig := &CreateServerParams{
		ConfigCallback: discoveryConfig,
	}
	paramsMap := map[int]*CreateServerParams{
		0: defaultConfig,
		1: defaultConfig,
		2: defaultConfig,
	}

	servers, createErr := createServers(3, paramsMap)
	if createErr != nil {
		t.Fatalf("Unable to create servers, %v", createErr)
	}

	t.Cleanup(func() {
		closeTestServers(t, servers)
	})

	// Server 0 -> Server 1
	if joinErr := JoinAndWait(servers[0], servers[1], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join peers, %v", joinErr)
	}

	// Server 1 -> Server 2
	if joinErr := JoinAndWait(servers[1], servers[2], DefaultBufferTimeout, DefaultJoinTimeout); joinErr != nil {
		t.Fatalf("Unable to join peers, %v", joinErr)
	}

	// Wait until Server 0 connects to Server 2 by discovery
	discoveryTimeout := time.Second * 25

	connectCtx, connectFn := context.WithTimeout(context.Background(), discoveryTimeout)
	defer connectFn()

	if _, connectErr := WaitUntilPeerConnectsTo(
		connectCtx,
		servers[0],
		servers[2].AddrInfo().ID,
	); connectErr != nil {
		t.Fatalf("Unable to connect to peer, %v", connectErr)
	}

	// Check that all peers are connected to each other
	for _, server := range servers {
		assert.Len(t, server.host.Peerstore().Peers(), 3)
	}
}
