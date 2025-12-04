package extproc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/structpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

type server struct{}
type healthServer struct{}

// extractUpstreamIP extracts the upstream IP address from request attributes
func extractUpstreamIP(attributes map[string]*structpb.Struct) string {
	if attributes == nil {
		return ""
	}

	filterAttributes := attributes["envoy.filters.http.ext_proc"]
	if filterAttributes == nil || filterAttributes.Fields == nil {
		return ""
	}

	// Try upstream.address first (available in upstream filter during request)
	if upstream, ok := filterAttributes.Fields["upstream.address"]; ok {
		if upstream != nil {
			upstreamAddr := upstream.GetStringValue()
			if upstreamAddr != "" {
				return strings.Split(upstreamAddr, ":")[0]
			}
		}
	}
	return ""
}

// isUpstreamIPSafe checks if the upstream IP is safe to connect to
// Returns true if safe, false if the IP should be blocked
func isUpstreamIPSafe(ipStr string) (bool, string) {
	if ipStr == "" {
		return false, "empty IP address"
	}

	// Parse the IP address
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false, "invalid IP address"
	}

	// Block localhost and loopback addresses
	if ip.IsLoopback() {
		return false, "localhost/loopback address is blocked"
	}

	// Block unspecified addresses (0.0.0.0 or ::)
	if ip.IsUnspecified() {
		return false, "unspecified address is blocked"
	}

	// Block link-local addresses (169.254.0.0/16 for IPv4, fe80::/10 for IPv6)
	if ip.IsLinkLocalUnicast() {
		return false, "link-local address is blocked"
	}

	// Block multicast addresses
	if ip.IsMulticast() {
		return false, "multicast address is blocked"
	}

	// Block private network ranges
	if ip.IsPrivate() {
		return false, "private network address is blocked (RFC1918)"
	}

	// Check for cloud metadata service IPs
	// AWS metadata service: 169.254.169.254
	if ipStr == "169.254.169.254" {
		return false, "AWS metadata service IP is blocked"
	}

	// GCP metadata service: 169.254.169.254 (same as AWS)
	// Azure metadata service: 169.254.169.254 (same as AWS)
	// All major cloud providers use the same IP

	// Additional IPv6 link-local checks for cloud metadata
	// GCP also uses fd00:ec2::254
	if ipStr == "fd00:ec2::254" {
		return false, "GCP metadata service IPv6 is blocked"
	}

	// Block IPv4-mapped IPv6 addresses that map to blocked ranges
	if ip.To4() == nil && ip.To16() != nil {
		// Check if it's an IPv4-mapped IPv6 address
		if strings.HasPrefix(ipStr, "::ffff:") {
			// Extract the IPv4 part and check it
			ipv4Part := strings.TrimPrefix(ipStr, "::ffff:")
			if safe, reason := isUpstreamIPSafe(ipv4Part); !safe {
				return false, fmt.Sprintf("IPv4-mapped IPv6 address blocked: %s", reason)
			}
		}
	}

	// Block documentation/example ranges
	// IPv4: 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 (TEST-NET-1,2,3)
	// IPv6: 2001:db8::/32 (documentation)
	_, testNet1, _ := net.ParseCIDR("192.0.2.0/24")
	_, testNet2, _ := net.ParseCIDR("198.51.100.0/24")
	_, testNet3, _ := net.ParseCIDR("203.0.113.0/24")
	_, testNet6, _ := net.ParseCIDR("2001:db8::/32")

	if testNet1.Contains(ip) || testNet2.Contains(ip) || testNet3.Contains(ip) || testNet6.Contains(ip) {
		return false, "documentation/test network range is blocked"
	}

	// If all checks pass, the IP is considered safe
	return true, ""
}

func (s *healthServer) Check(ctx context.Context, in *healthPb.HealthCheckRequest) (*healthPb.HealthCheckResponse, error) {
	log.Printf("Handling grpc Check request + %s", in.String())
	return &healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING}, nil
}

func (s *healthServer) Watch(in *healthPb.HealthCheckRequest, srv healthPb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}

// Demo Ext-Proc server
func (s *server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	ctx := srv.Context()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := srv.Recv()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", err)
		}

		var resp *extProcPb.ProcessingResponse
		upstreamIP := extractUpstreamIP(req.Attributes)
		isSafe := false
		reason := ""

		if upstreamIP != "" {
			log.Printf("Upstream IP Address: %s\n", upstreamIP)

			// Check if the upstream IP is safe
			isSafe, reason = isUpstreamIPSafe(upstreamIP)
		} else {
			isSafe = false
			reason = "unable to extract upstream IP address"
		}

		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			// Extract upstream IP address from attributes

			if !isSafe {
				log.Printf("BLOCKED: Upstream IP %s - %s\n", upstreamIP, reason)

				// Return immediate response that denies the request
				resp = &extProcPb.ProcessingResponse{
					Response: &extProcPb.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: &extProcPb.ImmediateResponse{
							Status: &typev3.HttpStatus{
								Code: typev3.StatusCode_Forbidden,
							},
							Body: []byte(reason),
						},
					},
					// Optionally, set dynamic metadata to indicate blocking
					DynamicMetadata: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"blocked": structpb.NewBoolValue(true),
							"reason":  structpb.NewStringValue(reason),
						},
					},
				}
			} else {
				log.Printf("ALLOWED: Upstream IP %s\n", upstreamIP)
				resp = &extProcPb.ProcessingResponse{
					Response: &extProcPb.ProcessingResponse_RequestHeaders{
						RequestHeaders: &extProcPb.HeadersResponse{
							Response: &extProcPb.CommonResponse{
								Status: extProcPb.CommonResponse_CONTINUE,
							},
						},
					},
				}
			}

		default:
			log.Printf("Unexpected Request type %+v\n", v)
		}

		if err := srv.Send(resp); err != nil {
			log.Printf("send error %v", err)
		}
	}
}

// Run entry point for Envoy XDS command line.
func Run() error {
	grpcServer := grpc.NewServer()
	reflection.Register(grpcServer)
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", config.Port))
	if err != nil {
		log.Fatal(err)
	}

	extProcPb.RegisterExternalProcessorServer(grpcServer, &server{})
	healthPb.RegisterHealthServer(grpcServer, &healthServer{})

	go func() {
		if err = grpcServer.Serve(lis); err != nil {
			log.Fatal(err)
		}
	}()

	log.Infof("Listening on %d", config.Port)

	// Wait for CTRL-c shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	<-done

	grpcServer.GracefulStop()
	log.Info("Shutdown")
	return nil
}
