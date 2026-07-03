package wire

import "testing"

func TestBuildConnectDPBIncludesDataTypeBindAndSessionTimeZone(t *testing.T) {
	cfg := &ProtocolConfig{
		User:            "sysdba",
		Charset:         "UTF8",
		Dialect:         SQLDialectCurrent,
		AuthPluginList:  DefaultPluginList,
		DataTypeBind:    "TIME ZONE TO EXTENDED",
		SessionTimeZone: "America/New_York",
	}
	srp := &srpClient{pluginName: PluginSrp256}

	dpb := buildConnectDPB(cfg, srp)

	if got, ok := dpbStringValue(dpb, IscDpbSetBind); !ok || got != "TIME ZONE TO EXTENDED" {
		t.Fatalf("isc_dpb_set_bind = %q, %v; want TIME ZONE TO EXTENDED, true", got, ok)
	}
	if got, ok := dpbStringValue(dpb, IscDpbSessionTimeZone); !ok || got != "America/New_York" {
		t.Fatalf("isc_dpb_session_time_zone = %q, %v; want America/New_York, true", got, ok)
	}
}

func TestBuildConnectDPBOmitsServerSessionTimeZone(t *testing.T) {
	cfg := &ProtocolConfig{
		User:            "sysdba",
		Charset:         "UTF8",
		Dialect:         SQLDialectCurrent,
		AuthPluginList:  DefaultPluginList,
		SessionTimeZone: "server",
	}
	srp := &srpClient{pluginName: PluginSrp256}

	dpb := buildConnectDPB(cfg, srp)

	if got, ok := dpbStringValue(dpb, IscDpbSessionTimeZone); ok {
		t.Fatalf("isc_dpb_session_time_zone = %q, true; want omitted", got)
	}
}

func dpbStringValue(dpb []byte, tag byte) (string, bool) {
	if len(dpb) == 0 || dpb[0] != IscDpbVersion2 {
		return "", false
	}
	for pos := 1; pos < len(dpb); {
		if pos+5 > len(dpb) {
			return "", false
		}
		itemTag := dpb[pos]
		length := int(dpb[pos+1]) |
			int(dpb[pos+2])<<8 |
			int(dpb[pos+3])<<16 |
			int(dpb[pos+4])<<24
		pos += 5
		if length < 0 || pos+length > len(dpb) {
			return "", false
		}
		value := dpb[pos : pos+length]
		pos += length
		if itemTag == tag {
			return string(value), true
		}
	}
	return "", false
}
