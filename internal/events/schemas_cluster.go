package events

const (
	EventClusterNodeJoined          = "cluster.node.joined"
	EventClusterNodeLost            = "cluster.node.lost"
	EventClusterNodeDraining        = "cluster.node.draining"
	EventClusterLeaderElected       = "cluster.leader.elected"
	EventClusterDatabaseUnavailable = "cluster.database.unavailable"
	EventClusterDatabaseRecovered   = "cluster.database.recovered"
)

type ClusterNodeJoinedPayload struct {
	NodeID      string   `json:"nodeId" event:"label=Node ID;desc=Unique cluster node identifier;example=node-b"`
	NodeName    string   `json:"nodeName" event:"label=Node Name;desc=Display name of the joining node;example=media-2"`
	Version     string   `json:"version,omitempty" event:"label=Version;desc=Ether version running on the node;example=0.3.17"`
	Roles       []string `json:"roles,omitempty" event:"label=Roles;desc=Roles advertised by the node;example=server,worker"`
	MemberCount int      `json:"memberCount" event:"label=Member Count;desc=Live cluster members after the join;example=2"`
}

type ClusterNodeLostPayload struct {
	NodeID      string   `json:"nodeId" event:"label=Node ID;desc=Unique identifier of the lost node;example=node-b"`
	NodeName    string   `json:"nodeName" event:"label=Node Name;desc=Display name of the lost node;example=media-2"`
	LastSeenMS  int64    `json:"lastSeenMs" event:"label=Since Last Seen;desc=Milliseconds elapsed since the node last heartbeated;duration_ms;unit=ms;example=32000"`
	Roles       []string `json:"roles,omitempty" event:"label=Roles;desc=Roles held by the lost node;example=worker"`
	MemberCount int      `json:"memberCount" event:"label=Member Count;desc=Remaining live cluster members;example=1"`
}

type ClusterNodeDrainingPayload struct {
	NodeID         string `json:"nodeId" event:"label=Node ID;desc=Unique identifier of the draining node;example=node-a"`
	NodeName       string `json:"nodeName" event:"label=Node Name;desc=Display name of the draining node;example=media-1"`
	Reason         string `json:"reason" event:"label=Reason;desc=Reason the node entered drain mode;example=rolling_update"`
	ActiveSessions int    `json:"activeSessions" event:"label=Active Sessions;desc=Number of playback sessions still active on the node;example=3"`
}

type ClusterLeaderElectedPayload struct {
	LeaderNodeID     string `json:"leaderNodeId" event:"label=Leader Node ID;desc=Node identifier of the newly elected leader;example=node-a"`
	LeaderName       string `json:"leaderName" event:"label=Leader Name;desc=Display name of the newly elected leader;example=media-1"`
	Epoch            int64  `json:"epoch" event:"label=Epoch;desc=Monotonic leadership term number;example=12"`
	PreviousLeaderID string `json:"previousLeaderId,omitempty" event:"label=Previous Leader ID;desc=Node identifier of the prior leader if known;example=node-b"`
}

type ClusterDatabaseUnavailablePayload struct {
	Dialect string `json:"dialect" event:"label=Dialect;desc=Database backend dialect;example=postgres"`
	Error   string `json:"error" event:"label=Error;desc=Last database connectivity error;example=connection refused"`
}

type ClusterDatabaseRecoveredPayload struct {
	Dialect          string `json:"dialect" event:"label=Dialect;desc=Database backend dialect;example=postgres"`
	OutageDurationMS int64  `json:"outageDurationMs" event:"label=Outage Duration;desc=Duration the database was unreachable in milliseconds;duration_ms;unit=ms;example=18400"`
	ReplayedWrites   int64  `json:"replayedWrites" event:"label=Replayed Writes;desc=Count of held writes flushed on recovery;example=14"`
}

func init() {
	registerBuiltin(RegisterClusterSchemas)
}

// RegisterClusterSchemas registers all cluster domain event schemas on r.
func RegisterClusterSchemas(r *Registry) {
	mustRegisterBuiltin[ClusterNodeJoinedPayload](r, EventSchema{
		Type:        EventClusterNodeJoined,
		Label:       "Cluster Node Joined",
		Description: "A node joined or returned to the Ether cluster.",
		Sample: ClusterNodeJoinedPayload{
			NodeID:      "node-b",
			NodeName:    "media-2",
			Version:     "0.3.17",
			Roles:       []string{"server", "worker"},
			MemberCount: 2,
		},
	})

	mustRegisterBuiltin[ClusterNodeLostPayload](r, EventSchema{
		Type:        EventClusterNodeLost,
		Label:       "Cluster Node Lost",
		Description: "A cluster node stopped heartbeating and was marked offline.",
		Critical:    true,
		Sample: ClusterNodeLostPayload{
			NodeID:      "node-b",
			NodeName:    "media-2",
			LastSeenMS:  32000,
			Roles:       []string{"worker"},
			MemberCount: 1,
		},
	})

	mustRegisterBuiltin[ClusterNodeDrainingPayload](r, EventSchema{
		Type:        EventClusterNodeDraining,
		Label:       "Cluster Node Draining",
		Description: "A cluster node entered drain mode for maintenance or rolling restart.",
		Sample: ClusterNodeDrainingPayload{
			NodeID:         "node-a",
			NodeName:       "media-1",
			Reason:         "rolling_update",
			ActiveSessions: 3,
		},
	})

	mustRegisterBuiltin[ClusterLeaderElectedPayload](r, EventSchema{
		Type:        EventClusterLeaderElected,
		Label:       "Cluster Leader Elected",
		Description: "A node acquired cluster leadership.",
		Sample: ClusterLeaderElectedPayload{
			LeaderNodeID:     "node-a",
			LeaderName:       "media-1",
			Epoch:            12,
			PreviousLeaderID: "node-b",
		},
	})

	mustRegisterBuiltin[ClusterDatabaseUnavailablePayload](r, EventSchema{
		Type:        EventClusterDatabaseUnavailable,
		Label:       "Database Unavailable",
		Description: "The shared cluster database became unreachable.",
		Critical:    true,
		Sample: ClusterDatabaseUnavailablePayload{
			Dialect: "postgres",
			Error:   "connection refused",
		},
	})

	mustRegisterBuiltin[ClusterDatabaseRecoveredPayload](r, EventSchema{
		Type:        EventClusterDatabaseRecovered,
		Label:       "Database Recovered",
		Description: "Connectivity to the shared cluster database was restored.",
		Sample: ClusterDatabaseRecoveredPayload{
			Dialect:          "postgres",
			OutageDurationMS: 18400,
			ReplayedWrites:   14,
		},
	})
}
