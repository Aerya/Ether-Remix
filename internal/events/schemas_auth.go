package events

import "time"

const (
	EventAuthLoginSucceeded             = "auth.login.succeeded"
	EventAuthLoginFailed                = "auth.login.failed"
	EventAuthGrantRevoked               = "auth.grant.revoked"
	EventAuthDeviceRegistered           = "auth.device.registered"
	EventAuthDeviceQuickConnectApproved = "auth.device.quickconnect_approved"
	EventAuthSessionNewIP               = "auth.session.new_ip"
	EventAuthTokenExpired               = "auth.token.expired"
	EventAuthRateLimited                = "auth.rate.limited"
	EventAuthCertificateRenewed         = "auth.certificate.renewed"
)

type AuthLoginSucceededPayload struct {
	ViewerID int64  `json:"viewerId" event:"label=Viewer ID;desc=Authenticated viewer account ID;example=1"`
	Username string `json:"username" event:"label=Username;desc=Authenticated viewer username;pii;example=alex"`
	Method   string `json:"method" event:"label=Method;desc=Authentication method used;example=password"`
	Client   string `json:"client,omitempty" event:"label=Client;desc=Client application name;example=Vela"`
	Platform string `json:"platform,omitempty" event:"label=Platform;desc=Client operating system or platform;example=tvOS"`
	RemoteIP string `json:"remoteIP,omitempty" event:"label=Remote IP;desc=Client network address;pii;example=203.0.113.42"`
}

type AuthLoginFailedPayload struct {
	Username string `json:"username,omitempty" event:"label=Username;desc=Attempted username if provided;pii;example=alex"`
	Method   string `json:"method" event:"label=Method;desc=Authentication method attempted;example=password"`
	Reason   string `json:"reason" event:"label=Reason;desc=Failure classification;example=invalid_credentials"`
	Client   string `json:"client,omitempty" event:"label=Client;desc=Client application name;example=Ether browser"`
	Platform string `json:"platform,omitempty" event:"label=Platform;desc=Client platform;example=Web"`
	RemoteIP string `json:"remoteIP,omitempty" event:"label=Remote IP;desc=Client network address;pii;example=203.0.113.42"`
}

type AuthGrantRevokedPayload struct {
	ViewerID  int64  `json:"viewerId" event:"label=Viewer ID;desc=Viewer whose grant or session was revoked;example=7"`
	GrantKind string `json:"grantKind" event:"label=Grant Kind;desc=Scope of revoked credential;example=device"`
	Reason    string `json:"reason,omitempty" event:"label=Reason;desc=Reason for revocation;example=sign_out"`
	Actor     string `json:"actor,omitempty" event:"label=Actor;desc=Actor or subsystem that revoked the grant;example=owner"`
}

type AuthDeviceRegisteredPayload struct {
	DeviceID             string `json:"deviceId" event:"label=Device ID;desc=Assigned Ether device identifier;example=dev_14"`
	ClientIdentifier     string `json:"clientIdentifier,omitempty" event:"label=Client Identifier;desc=Stable client-reported identifier;example=appletv-living-room"`
	Name                 string `json:"name,omitempty" event:"label=Device Name;desc=Human-readable device name;example=Living Room Apple TV"`
	Platform             string `json:"platform,omitempty" event:"label=Platform;desc=Client operating system or platform;example=tvOS"`
	ViewerID             int64  `json:"viewerId,omitempty" event:"label=Viewer ID;desc=Viewer associated with the device;example=7"`
	Username             string `json:"username,omitempty" event:"label=Username;desc=Username associated with the device;pii;example=alex"`
	RemoteIP             string `json:"remoteIP,omitempty" event:"label=Remote IP;desc=Network address seen during registration;pii;example=192.168.1.44"`
	IsFirstSeenForViewer bool   `json:"isFirstSeenForViewer" event:"label=First Seen For Viewer;desc=True when this device was not previously seen for the viewer;example=true"`
}

type AuthDeviceQuickConnectApprovedPayload struct {
	ViewerID   int64  `json:"viewerId" event:"label=Viewer ID;desc=Viewer who approved the Quick Connect pairing;example=7"`
	DeviceName string `json:"deviceName,omitempty" event:"label=Device Name;desc=Paired device name;example=Living Room TV"`
	Platform   string `json:"platform,omitempty" event:"label=Platform;desc=Paired device platform or surface;example=jellyfin"`
}

type AuthSessionNewIPPayload struct {
	ViewerID          int64    `json:"viewerId" event:"label=Viewer ID;desc=Viewer connecting from an unfamiliar IP;example=7"`
	Username          string   `json:"username" event:"label=Username;desc=Viewer display name or username;pii;example=alex"`
	RemoteIP          string   `json:"remoteIP" event:"label=Remote IP;desc=New network address observed;pii;example=198.51.100.24"`
	PreviousIPs       []string `json:"previousIPs,omitempty" event:"label=Previous IPs;desc=Previously observed network addresses for this viewer;pii;example=203.0.113.10"`
	Location          string   `json:"location" event:"label=Location;desc=Whether the new IP is on the local network or WAN;enum=lan|wan;example=wan"`
	Country           string   `json:"country,omitempty" event:"label=Country;desc=GeoIP country if available;example=US"`
	City              string   `json:"city,omitempty" event:"label=City;desc=GeoIP city if available;example=Seattle"`
	ASN               string   `json:"asn,omitempty" event:"label=ASN;desc=Autonomous system number if available;example=AS7922"`
	Org               string   `json:"org,omitempty" event:"label=Organization;desc=Network operator name if available;example=Example ISP"`
	Client            string   `json:"client,omitempty" event:"label=Client;desc=Client application name;example=Plex for iOS"`
	Platform          string   `json:"platform,omitempty" event:"label=Platform;desc=Client platform;example=iOS"`
	SessionID         string   `json:"sessionId,omitempty" event:"label=Session ID;desc=Playback or auth session identifier;example=sess_01"`
	DistinctActiveIPs int      `json:"distinctActiveIPs" event:"label=Distinct Active IPs;desc=Count of distinct IPs currently active for this viewer;example=2"`
}

type AuthTokenExpiredPayload struct {
	Provider  string `json:"provider" event:"label=Provider;desc=External service whose token expired or was revoked;example=trakt"`
	Scope     string `json:"scope,omitempty" event:"label=Scope;desc=Integration scope affected by the expired token;example=watch_sync"`
	AccountID string `json:"accountId,omitempty" event:"label=Account ID;desc=Provider account identifier if known;example=trakt:7"`
	ViewerID  int64  `json:"viewerId,omitempty" event:"label=Viewer ID;desc=Viewer owning the expired token;example=7"`
	Username  string `json:"username,omitempty" event:"label=Username;desc=Username owning the expired token;pii;example=alex"`
	Error     string `json:"error" event:"label=Error;desc=Rejection or expiration error reported by the provider;example=refresh token revoked"`
}

type AuthRateLimitedPayload struct {
	Scope     string `json:"scope" event:"label=Scope;desc=Authentication endpoint or surface that tripped rate limiting;example=login"`
	RemoteIP  string `json:"remoteIP" event:"label=Remote IP;desc=Client IP or network prefix that exceeded the limit;pii;example=203.0.113.99"`
	Attempts  int    `json:"attempts" event:"label=Attempts;desc=Number of attempts observed in the current window;example=21"`
	WindowSec int    `json:"windowSec" event:"label=Window;desc=Rate-limit sliding window duration in seconds;unit=s;example=300"`
}

type AuthCertificateRenewedPayload struct {
	Domains   []string  `json:"domains" event:"label=Domains;desc=DNS names covered by the renewed TLS certificate;example=*.1-2-3-4.abc.plex.direct"`
	Issuer    string    `json:"issuer,omitempty" event:"label=Issuer;desc=Certificate authority or subject issuer;example=R11"`
	ExpiresAt time.Time `json:"expiresAt,omitzero" event:"label=Expires At;desc=Expiration timestamp of the renewed certificate;example=2027-02-19T00:00:00Z"`
}

func init() {
	registerBuiltin(RegisterAuthSchemas)
}

// RegisterAuthSchemas registers all auth domain event schemas on r.
func RegisterAuthSchemas(r *Registry) {
	sampleExpiry := time.Date(2027, 2, 19, 0, 0, 0, 0, time.UTC)

	mustRegisterBuiltin[AuthLoginSucceededPayload](r, EventSchema{
		Type:        EventAuthLoginSucceeded,
		Label:       "Login Succeeded",
		Description: "A viewer or owner completed sign-in.",
		Sample: AuthLoginSucceededPayload{
			ViewerID: 1,
			Username: "alex",
			Method:   "password",
			Client:   "Vela",
			Platform: "tvOS",
			RemoteIP: "203.0.113.42",
		},
	})

	mustRegisterBuiltin[AuthLoginFailedPayload](r, EventSchema{
		Type:        EventAuthLoginFailed,
		Label:       "Login Failed",
		Description: "A sign-in attempt was rejected.",
		Sample: AuthLoginFailedPayload{
			Username: "alex",
			Method:   "password",
			Reason:   "invalid_credentials",
			Client:   "Ether browser",
			Platform: "Web",
			RemoteIP: "203.0.113.42",
		},
	})

	mustRegisterBuiltin[AuthGrantRevokedPayload](r, EventSchema{
		Type:        EventAuthGrantRevoked,
		Label:       "Grant Revoked",
		Description: "An authentication grant, device token, or session was revoked.",
		Sample: AuthGrantRevokedPayload{
			ViewerID:  7,
			GrantKind: "device",
			Reason:    "sign_out",
			Actor:     "owner",
		},
	})

	mustRegisterBuiltin[AuthDeviceRegisteredPayload](r, EventSchema{
		Type:        EventAuthDeviceRegistered,
		Label:       "Device Registered",
		Description: "A client device connected to Ether for the first time.",
		Sample: AuthDeviceRegisteredPayload{
			DeviceID:             "dev_14",
			ClientIdentifier:     "appletv-living-room",
			Name:                 "Living Room Apple TV",
			Platform:             "tvOS",
			ViewerID:             7,
			Username:             "alex",
			RemoteIP:             "192.168.1.44",
			IsFirstSeenForViewer: true,
		},
	})

	mustRegisterBuiltin[AuthDeviceQuickConnectApprovedPayload](r, EventSchema{
		Type:        EventAuthDeviceQuickConnectApproved,
		Label:       "Quick Connect Approved",
		Description: "A viewer approved a Quick Connect device pairing code.",
		Sample: AuthDeviceQuickConnectApprovedPayload{
			ViewerID:   7,
			DeviceName: "Living Room TV",
			Platform:   "jellyfin",
		},
	})

	mustRegisterBuiltin[AuthSessionNewIPPayload](r, EventSchema{
		Type:        EventAuthSessionNewIP,
		Label:       "Session From New IP",
		Description: "A viewer started a session from an IP address not previously seen for their account.",
		Sample: AuthSessionNewIPPayload{
			ViewerID:          7,
			Username:          "alex",
			RemoteIP:          "198.51.100.24",
			PreviousIPs:       []string{"203.0.113.10"},
			Location:          "wan",
			Client:            "Plex for iOS",
			Platform:          "iOS",
			SessionID:         "sess_01",
			DistinctActiveIPs: 2,
		},
	})

	mustRegisterBuiltin[AuthTokenExpiredPayload](r, EventSchema{
		Type:        EventAuthTokenExpired,
		Label:       "External Token Expired",
		Description: "An external integration credential expired or was revoked and requires reconnection.",
		Critical:    true,
		Sample: AuthTokenExpiredPayload{
			Provider:  "trakt",
			Scope:     "watch_sync",
			AccountID: "trakt:7",
			ViewerID:  7,
			Username:  "alex",
			Error:     "refresh token revoked",
		},
	})

	mustRegisterBuiltin[AuthRateLimitedPayload](r, EventSchema{
		Type:        EventAuthRateLimited,
		Label:       "Auth Rate Limited",
		Description: "Repeated authentication attempts tripped rate limiting.",
		Critical:    true,
		Sample: AuthRateLimitedPayload{
			Scope:     "login",
			RemoteIP:  "203.0.113.99",
			Attempts:  21,
			WindowSec: 300,
		},
	})

	mustRegisterBuiltin[AuthCertificateRenewedPayload](r, EventSchema{
		Type:        EventAuthCertificateRenewed,
		Label:       "TLS Certificate Renewed",
		Description: "Ether renewed or installed a TLS certificate.",
		LegacyNames: []string{"cert.renewed"},
		Sample: AuthCertificateRenewedPayload{
			Domains:   []string{"*.1-2-3-4.abc.plex.direct"},
			Issuer:    "R11",
			ExpiresAt: sampleExpiry,
		},
	})
}
