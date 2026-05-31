package xray

import "vectra-controller-pro/internal/config"

// buildStream translates config.StreamSettings into Xray's streamSettings.
func buildStream(s *config.StreamSettings) *xStreamSettings {
	if s == nil {
		return nil
	}
	out := &xStreamSettings{
		Network:  s.Transport,
		Security: s.Security,
	}
	switch s.Transport {
	case "tcp":
		if s.TCP != nil {
			tcp := &xTCPSettings{AcceptProxyProtocol: s.TCP.AcceptProxyProtocol}
			if s.TCP.Header != nil {
				tcp.Header = &xTCPHeader{
					Type:     s.TCP.Header.Type,
					Request:  s.TCP.Header.Request,
					Response: s.TCP.Header.Response,
				}
			}
			out.TCPSettings = tcp
		}
	case "ws":
		if s.WS != nil {
			out.WSSettings = &xWSSettings{
				Path:                s.WS.Path,
				Host:                s.WS.Host,
				Headers:             s.WS.Headers,
				AcceptProxyProtocol: s.WS.AcceptProxyProtocol,
				HeartbeatPeriod:     s.WS.HeartbeatPeriod,
			}
		}
	case "grpc":
		if s.GRPC != nil {
			out.GRPCSettings = &xGRPCSettings{
				ServiceName:         s.GRPC.ServiceName,
				Authority:           s.GRPC.Authority,
				MultiMode:           s.GRPC.MultiMode,
				IdleTimeout:         s.GRPC.IdleTimeout,
				HealthCheckTimeout:  s.GRPC.HealthCheckTimeout,
				PermitWithoutStream: s.GRPC.PermitWithoutStream,
				InitialWindowsSize:  s.GRPC.InitialWindowsSize,
				UserAgent:           s.GRPC.UserAgent,
			}
		}
	case "kcp":
		if s.KCP != nil {
			kcp := &xKCPSettings{
				MTU:              s.KCP.MTU,
				TTI:              s.KCP.TTI,
				UplinkCapacity:   s.KCP.UplinkCapacity,
				DownlinkCapacity: s.KCP.DownlinkCapacity,
				Congestion:       s.KCP.Congestion,
				ReadBufferSize:   s.KCP.ReadBufferSize,
				WriteBufferSize:  s.KCP.WriteBufferSize,
				Seed:             s.KCP.Seed,
			}
			if s.KCP.Header != nil {
				kcp.Header = &xKCPHeader{Type: s.KCP.Header.Type}
			}
			out.KCPSettings = kcp
		}
	case "quic":
		if s.QUIC != nil {
			q := &xQUICSettings{Security: s.QUIC.Security, Key: s.QUIC.Key}
			if s.QUIC.Header != nil {
				q.Header = &xQUICHeader{Type: s.QUIC.Header.Type}
			}
			out.QUICSettings = q
		}
	case "xhttp":
		if s.XHTTP != nil {
			out.XHTTPSettings = &xXHTTPSettings{
				Path: s.XHTTP.Path, Host: s.XHTTP.Host, Headers: s.XHTTP.Headers,
				Mode: s.XHTTP.Mode, Extra: s.XHTTP.Extra,
			}
		}
	case "httpupgrade":
		if s.HTTPUpgrade != nil {
			out.HTTPUpgradeSettings = &xHTTPUpgradeSettings{
				Path: s.HTTPUpgrade.Path, Host: s.HTTPUpgrade.Host, Headers: s.HTTPUpgrade.Headers,
				AcceptProxyProtocol: s.HTTPUpgrade.AcceptProxyProtocol,
			}
		}
	case "domainsocket":
		if s.DS != nil {
			out.DSSettings = &xDSSettings{Path: s.DS.Path, Abstract: s.DS.Abstract, Padding: s.DS.Padding}
		}
	}
	switch s.Security {
	case "tls":
		if s.TLS != nil {
			out.TLSSettings = &xTLSSettings{
				ServerName:                       s.TLS.ServerName,
				AllowInsecure:                    s.TLS.AllowInsecure,
				ALPN:                             s.TLS.ALPN,
				Fingerprint:                      s.TLS.Fingerprint,
				EnableSessionResumption:          s.TLS.EnableSessionResumption,
				CurvePreferences:                 s.TLS.CurvePreferences,
				DisableSystemRoot:                s.TLS.DisableSystemRoot,
				MasterKeyLog:                     s.TLS.MasterKeyLog,
				PinnedPeerCertificateChainSha256: s.TLS.PinnedPeerCertificateChainSha256,
				CipherSuites:                     s.TLS.CipherSuites,
				MinVersion:                       s.TLS.MinVersion,
				MaxVersion:                       s.TLS.MaxVersion,
				RejectUnknownSNI:                 s.TLS.RejectUnknownSNI,
				ECHConfig:                        s.TLS.ECHConfig,
				ECHServerKeys:                    s.TLS.ECHServerKeys,
				NextProtos:                       s.TLS.NextProtos,
				VerifyPeerCertInNames:            s.TLS.VerifyPeerCertInNames,
			}
			for _, ct := range s.TLS.Certificates {
				out.TLSSettings.Certificates = append(out.TLSSettings.Certificates, xTLSCertificate{
					Usage:           ct.Usage,
					Certificate:     ct.Certificate,
					Key:             ct.Key,
					CertificateFile: ct.CertificateFile,
					KeyFile:         ct.KeyFile,
					OCSPStapling:    ct.OCSPStapling,
				})
			}
		}
	case "reality":
		if s.REALITY != nil {
			out.RealitySettings = &xRealitySettings{
				Show:        s.REALITY.Show,
				Fingerprint: s.REALITY.Fingerprint,
				ServerName:  s.REALITY.ServerName,
				PublicKey:   s.REALITY.PublicKey,
				ShortID:     s.REALITY.ShortID,
				SpiderX:     s.REALITY.SpiderX,
				MaxTimeDiff: s.REALITY.MaxTimeDiff,
			}
		}
	}
	if s.Sockopt != nil {
		out.Sockopt = &xSockopt{
			Mark:                 s.Sockopt.Mark,
			TCPFastOpen:          s.Sockopt.TCPFastOpen,
			TCPFastOpenQueueLen:  s.Sockopt.TCPFastOpenQueueLen,
			TProxy:               s.Sockopt.TProxy,
			DomainStrategy:       s.Sockopt.DomainStrategy,
			DialerProxy:          s.Sockopt.DialerProxy,
			TCPKeepAliveInterval: s.Sockopt.TCPKeepAliveInterval,
			TCPKeepAliveIdle:     s.Sockopt.TCPKeepAliveIdle,
			TCPCongestion:        s.Sockopt.TCPCongestion,
			Interface:            s.Sockopt.Interface,
			V6Only:               s.Sockopt.V6Only,
			TCPMaxSeg:            s.Sockopt.TCPMaxSeg,
			Penetrate:            s.Sockopt.Penetrate,
			TCPMptcp:             s.Sockopt.TCPMptcp,
			CustomSockopt:        s.Sockopt.CustomSockopt,
		}
	}
	return out
}
