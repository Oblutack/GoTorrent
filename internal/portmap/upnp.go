package portmap

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	ssdpMulticastAddr = "239.255.255.250:1900"
	ssdpSearchTarget  = "urn:schemas-upnp-org:device:InternetGatewayDevice:1"
	ssdpTimeout       = 3 * time.Second

	maxDescriptionBytes  = 1 << 20 // a device description XML is a few KB in practice
	maxSOAPResponseBytes = 1 << 16
)

// wanServiceTypes are the service types IGD implementations advertise for
// the WAN-facing connection — WANIPConnection covers the common case
// (cable/DSL routers doing NAT), WANPPPConnection covers PPPoE gateways.
// Either version works the same way for AddPortMapping/DeletePortMapping/
// GetExternalIPAddress, so any match is accepted.
var wanServiceTypes = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

// upnpDevice is what discovery needs to remember about the gateway: where
// to POST SOAP requests, and which service type to address them to.
type upnpDevice struct {
	controlURL  string
	serviceType string
}

// upnpMapper speaks UPnP IGD's SOAP control protocol to one discovered
// device.
type upnpMapper struct {
	device     *upnpDevice
	internalIP net.IP // this machine's LAN IP, for NewInternalClient
}

// discoverUPnP finds an Internet Gateway Device on the local network (SSDP
// multicast, then fetching and parsing its device description XML) and
// returns a mapper ready to call AddPortMapping against it.
func discoverUPnP(ctx context.Context) (*upnpMapper, error) {
	location, err := discoverGatewayLocation(ctx)
	if err != nil {
		return nil, err
	}
	device, err := fetchDevice(ctx, location)
	if err != nil {
		return nil, err
	}
	localIP, err := outboundIP()
	if err != nil {
		return nil, err
	}
	return &upnpMapper{device: device, internalIP: localIP}, nil
}

// discoverGatewayLocation sends an SSDP M-SEARCH multicast and returns the
// LOCATION URL of the first InternetGatewayDevice that answers.
func discoverGatewayLocation(ctx context.Context) (string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return "", fmt.Errorf("portmap: opening SSDP socket: %w", err)
	}
	defer conn.Close()

	dst, err := net.ResolveUDPAddr("udp4", ssdpMulticastAddr)
	if err != nil {
		return "", fmt.Errorf("portmap: resolving SSDP multicast address: %w", err)
	}
	req := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: " + ssdpMulticastAddr + "\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 2\r\n" +
		"ST: " + ssdpSearchTarget + "\r\n\r\n"
	if _, err := conn.WriteToUDP([]byte(req), dst); err != nil {
		return "", fmt.Errorf("portmap: sending SSDP M-SEARCH: %w", err)
	}

	deadline := time.Now().Add(ssdpTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)

	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return "", fmt.Errorf("portmap: no SSDP response from any gateway: %w", err)
		}
		if loc := parseSSDPLocation(buf[:n]); loc != "" {
			return loc, nil
		}
		// A response with no (or an unparseable) LOCATION header — keep
		// listening for another reply until the deadline.
	}
}

// parseSSDPLocation extracts the LOCATION header from a raw SSDP response
// (an HTTP-response-shaped block of text over UDP, per the SSDP spec).
// Split out from discoverGatewayLocation so it can be tested against canned
// bytes without needing real multicast traffic.
func parseSSDPLocation(resp []byte) string {
	for _, line := range strings.Split(string(resp), "\r\n") {
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:idx]), "LOCATION") {
			return strings.TrimSpace(line[idx+1:])
		}
	}
	return ""
}

// --- device description ----------------------------------------------------

type deviceDescXML struct {
	URLBase string    `xml:"URLBase"`
	Device  deviceXML `xml:"device"`
}

type deviceXML struct {
	DeviceList  []deviceXML  `xml:"deviceList>device"`
	ServiceList []serviceXML `xml:"serviceList>service"`
}

type serviceXML struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

func fetchDevice(ctx context.Context, location string) (*upnpDevice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, fmt.Errorf("portmap: building request for %s: %w", location, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("portmap: fetching device description from %s: %w", location, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("portmap: %s returned HTTP %d", location, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDescriptionBytes))
	if err != nil {
		return nil, fmt.Errorf("portmap: reading device description: %w", err)
	}
	return parseDeviceDescription(body, location)
}

func parseDeviceDescription(body []byte, location string) (*upnpDevice, error) {
	var desc deviceDescXML
	if err := xml.Unmarshal(body, &desc); err != nil {
		return nil, fmt.Errorf("portmap: parsing device description: %w", err)
	}
	svc := findWANService(desc.Device)
	if svc == nil {
		return nil, errors.New("portmap: gateway's device description advertises no WANIPConnection/WANPPPConnection service")
	}

	base := desc.URLBase
	if base == "" {
		base = location
	}
	controlURL, err := resolveURL(base, svc.ControlURL)
	if err != nil {
		return nil, err
	}
	return &upnpDevice{controlURL: controlURL, serviceType: svc.ServiceType}, nil
}

// findWANService walks the device tree (a device can nest child devices,
// and the WAN connection service is typically two levels down under
// WANDevice > WANConnectionDevice) looking for one of wanServiceTypes.
func findWANService(d deviceXML) *serviceXML {
	for i := range d.ServiceList {
		for _, want := range wanServiceTypes {
			if d.ServiceList[i].ServiceType == want {
				return &d.ServiceList[i]
			}
		}
	}
	for _, child := range d.DeviceList {
		if s := findWANService(child); s != nil {
			return s
		}
	}
	return nil
}

func resolveURL(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("portmap: bad base URL %q: %w", base, err)
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("portmap: bad control URL %q: %w", ref, err)
	}
	return baseURL.ResolveReference(refURL).String(), nil
}

// --- SOAP control ------------------------------------------------------

type soapFaultXML struct {
	Body struct {
		Fault struct {
			Detail struct {
				UPnPError struct {
					ErrorCode        int    `xml:"errorCode"`
					ErrorDescription string `xml:"errorDescription"`
				} `xml:"UPnPError"`
			} `xml:"detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

// soapCall POSTs one SOAP action to controlURL and returns the response
// body on HTTP 200, or a descriptive error built from the SOAP fault
// otherwise.
func soapCall(ctx context.Context, controlURL, serviceType, action, argsXML string) ([]byte, error) {
	envelope := fmt.Sprintf(`<?xml version="1.0"?>`+
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`+
		`<s:Body><u:%s xmlns:u="%s">%s</u:%s></s:Body></s:Envelope>`,
		action, serviceType, argsXML, action)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, strings.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("portmap: building SOAP request for %s: %w", action, err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", fmt.Sprintf(`"%s#%s"`, serviceType, action))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("portmap: SOAP %s to %s: %w", action, controlURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSOAPResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("portmap: reading SOAP %s response: %w", action, err)
	}

	if resp.StatusCode != http.StatusOK {
		var fault soapFaultXML
		if xml.Unmarshal(body, &fault) == nil && fault.Body.Fault.Detail.UPnPError.ErrorCode != 0 {
			return nil, fmt.Errorf("portmap: UPnP %s failed: error %d: %s", action,
				fault.Body.Fault.Detail.UPnPError.ErrorCode, fault.Body.Fault.Detail.UPnPError.ErrorDescription)
		}
		return nil, fmt.Errorf("portmap: UPnP %s failed with HTTP %d", action, resp.StatusCode)
	}
	return body, nil
}

func (m *upnpMapper) name() string { return "UPnP" }

func (m *upnpMapper) addMapping(ctx context.Context, protocol string, internalPort uint16, lease time.Duration) (Mapping, error) {
	args := fmt.Sprintf(
		`<NewRemoteHost></NewRemoteHost>`+
			`<NewExternalPort>%d</NewExternalPort>`+
			`<NewProtocol>%s</NewProtocol>`+
			`<NewInternalPort>%d</NewInternalPort>`+
			`<NewInternalClient>%s</NewInternalClient>`+
			`<NewEnabled>1</NewEnabled>`+
			`<NewPortMappingDescription>GoTorrent</NewPortMappingDescription>`+
			`<NewLeaseDuration>%d</NewLeaseDuration>`,
		internalPort, protocol, internalPort, m.internalIP, int(lease.Seconds()))

	if _, err := soapCall(ctx, m.device.controlURL, m.device.serviceType, "AddPortMapping", args); err != nil {
		return Mapping{}, err
	}

	// AddPortMapping's response carries no fields of interest (an IGD does
	// not report back the port it granted the way NAT-PMP does — it either
	// honors the requested external port or fails the whole call), so the
	// external IP is fetched separately to build a complete Mapping.
	extIP, err := m.externalIP(ctx)
	if err != nil {
		return Mapping{}, err
	}
	return Mapping{ExternalIP: extIP, ExternalPort: internalPort, Protocol: protocol}, nil
}

func (m *upnpMapper) deleteMapping(ctx context.Context, protocol string, _, externalPort uint16) error {
	// UPnP's DeletePortMapping is keyed by external port and protocol, not
	// internal port — see the mapper interface's doc comment.
	args := fmt.Sprintf(
		`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>%s</NewProtocol>`,
		externalPort, protocol)
	_, err := soapCall(ctx, m.device.controlURL, m.device.serviceType, "DeletePortMapping", args)
	return err
}

func (m *upnpMapper) externalIP(ctx context.Context) (net.IP, error) {
	body, err := soapCall(ctx, m.device.controlURL, m.device.serviceType, "GetExternalIPAddress", "")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Body struct {
			GetExternalIPAddressResponse struct {
				NewExternalIPAddress string `xml:"NewExternalIPAddress"`
			} `xml:"GetExternalIPAddressResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("portmap: parsing GetExternalIPAddress response: %w", err)
	}
	raw := resp.Body.GetExternalIPAddressResponse.NewExternalIPAddress
	ip := net.ParseIP(raw)
	if ip == nil {
		return nil, fmt.Errorf("portmap: gateway returned an unparseable external IP %q", raw)
	}
	return ip, nil
}
