package portmap

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseSSDPLocation(t *testing.T) {
	resp := "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"LOCATION: http://192.168.1.1:5000/rootDesc.xml\r\n" +
		"ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n" +
		"\r\n"
	if got := parseSSDPLocation([]byte(resp)); got != "http://192.168.1.1:5000/rootDesc.xml" {
		t.Fatalf("got %q", got)
	}
}

func TestParseSSDPLocationIsCaseInsensitive(t *testing.T) {
	resp := "HTTP/1.1 200 OK\r\nlocation: http://10.0.0.1:1900/desc.xml\r\n\r\n"
	if got := parseSSDPLocation([]byte(resp)); got != "http://10.0.0.1:1900/desc.xml" {
		t.Fatalf("got %q", got)
	}
}

func TestParseSSDPLocationMissingHeaderReturnsEmpty(t *testing.T) {
	resp := "HTTP/1.1 200 OK\r\nST: something-else\r\n\r\n"
	if got := parseSSDPLocation([]byte(resp)); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// realWorldDeviceDescription mirrors the shape a typical home router
// advertises: the WAN connection service is nested two levels down, under
// a WANDevice and then a WANConnectionDevice, which is exactly the case
// findWANService's recursion exists for.
const realWorldDeviceDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
<device>
<deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
<deviceList>
<device>
<deviceType>urn:schemas-upnp-org:device:WANDevice:1</deviceType>
<deviceList>
<device>
<deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:1</deviceType>
<serviceList>
<service>
<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
<controlURL>/ctl/IPConn</controlURL>
</service>
</serviceList>
</device>
</deviceList>
</device>
</deviceList>
</device>
</root>`

func TestParseDeviceDescriptionFindsNestedWANService(t *testing.T) {
	device, err := parseDeviceDescription([]byte(realWorldDeviceDescription), "http://192.168.1.1:5000/rootDesc.xml")
	if err != nil {
		t.Fatalf("parseDeviceDescription: %v", err)
	}
	if device.serviceType != "urn:schemas-upnp-org:service:WANIPConnection:1" {
		t.Fatalf("serviceType = %q", device.serviceType)
	}
	if device.controlURL != "http://192.168.1.1:5000/ctl/IPConn" {
		t.Fatalf("controlURL = %q, want it resolved against the description's own location", device.controlURL)
	}
}

func TestParseDeviceDescriptionUsesURLBaseWhenPresent(t *testing.T) {
	xmlDoc := `<root><URLBase>http://192.168.1.1:49152/</URLBase><device>` +
		`<serviceList><service>` +
		`<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>` +
		`<controlURL>/upnp/control/WANIPConn1</controlURL>` +
		`</service></serviceList></device></root>`
	device, err := parseDeviceDescription([]byte(xmlDoc), "http://192.168.1.1:5000/rootDesc.xml")
	if err != nil {
		t.Fatalf("parseDeviceDescription: %v", err)
	}
	if device.controlURL != "http://192.168.1.1:49152/upnp/control/WANIPConn1" {
		t.Fatalf("controlURL = %q, want it resolved against URLBase, not the description location", device.controlURL)
	}
}

func TestParseDeviceDescriptionRejectsNonGateway(t *testing.T) {
	xmlDoc := `<root><device><deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType></device></root>`
	if _, err := parseDeviceDescription([]byte(xmlDoc), "http://x/desc.xml"); err == nil {
		t.Fatal("parseDeviceDescription accepted a device with no WAN service, want an error")
	}
}

// fakeIGD serves a device description and answers SOAP AddPortMapping/
// DeletePortMapping/GetExternalIPAddress requests over real HTTP, so
// upnpMapper is exercised end to end against the actual wire format rather
// than by calling its internals directly.
type fakeIGD struct {
	t          *testing.T
	srv        *httptest.Server
	externalIP string

	lastAddPortMappingBody string
	refuseAddPortMapping   bool
}

func newFakeIGD(t *testing.T, externalIP string) *fakeIGD {
	t.Helper()
	f := &fakeIGD{t: t, externalIP: externalIP}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIGD) device() *upnpDevice {
	return &upnpDevice{
		controlURL:  f.srv.URL + "/ctl/IPConn",
		serviceType: "urn:schemas-upnp-org:service:WANIPConnection:1",
	}
}

func (f *fakeIGD) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	action := r.Header.Get("SOAPAction")

	switch {
	case strings.Contains(action, "AddPortMapping"):
		f.lastAddPortMappingBody = string(body)
		if f.refuseAddPortMapping {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
<s:Body><s:Fault>
<faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring>
<detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">
<errorCode>718</errorCode><errorDescription>ConflictInMappingEntry</errorDescription>
</UPnPError></detail>
</s:Fault></s:Body></s:Envelope>`))
			return
		}
		w.Write([]byte(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
<s:Body><u:AddPortMappingResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/></s:Body>
</s:Envelope>`))

	case strings.Contains(action, "DeletePortMapping"):
		w.Write([]byte(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
<s:Body><u:DeletePortMappingResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/></s:Body>
</s:Envelope>`))

	case strings.Contains(action, "GetExternalIPAddress"):
		w.Write([]byte(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
<s:Body><u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">
<NewExternalIPAddress>` + f.externalIP + `</NewExternalIPAddress>
</u:GetExternalIPAddressResponse></s:Body>
</s:Envelope>`))

	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func TestUPnPAddMappingEndToEnd(t *testing.T) {
	igd := newFakeIGD(t, "203.0.113.9")
	m := &upnpMapper{device: igd.device(), internalIP: net.IPv4(192, 168, 1, 42)}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mapping, err := m.addMapping(ctx, "TCP", 6881, time.Hour)
	if err != nil {
		t.Fatalf("addMapping: %v", err)
	}
	if mapping.ExternalPort != 6881 || mapping.Protocol != "TCP" {
		t.Fatalf("got %+v", mapping)
	}
	if !mapping.ExternalIP.Equal(net.IPv4(203, 0, 113, 9)) {
		t.Fatalf("ExternalIP = %s, want 203.0.113.9", mapping.ExternalIP)
	}
	if !strings.Contains(igd.lastAddPortMappingBody, "<NewInternalClient>192.168.1.42</NewInternalClient>") {
		t.Fatalf("AddPortMapping body did not advertise the internal client IP: %s", igd.lastAddPortMappingBody)
	}
	if !strings.Contains(igd.lastAddPortMappingBody, "<NewInternalPort>6881</NewInternalPort>") {
		t.Fatalf("AddPortMapping body did not carry the internal port: %s", igd.lastAddPortMappingBody)
	}
}

func TestUPnPAddMappingSurfacesSOAPFault(t *testing.T) {
	igd := newFakeIGD(t, "203.0.113.9")
	igd.refuseAddPortMapping = true
	m := &upnpMapper{device: igd.device(), internalIP: net.IPv4(192, 168, 1, 42)}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.addMapping(ctx, "TCP", 6881, time.Hour)
	if err == nil {
		t.Fatal("addMapping succeeded despite a SOAP fault, want an error")
	}
	if !strings.Contains(err.Error(), "718") {
		t.Fatalf("error %q does not surface the UPnP error code", err)
	}
}

func TestUPnPDeleteMapping(t *testing.T) {
	igd := newFakeIGD(t, "203.0.113.9")
	m := &upnpMapper{device: igd.device(), internalIP: net.IPv4(192, 168, 1, 42)}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.deleteMapping(ctx, "TCP", 6881, 6881); err != nil {
		t.Fatalf("deleteMapping: %v", err)
	}
}
