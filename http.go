package armor

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/labstack/armor/util"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
	"github.com/labstack/tunnel-client"
	tutil "github.com/labstack/tunnel-client/util"
	"github.com/mitchellh/go-homedir"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

type (
	HTTP struct {
		armor  *Armor
		echo   *echo.Echo
		logger *log.Logger
	}
)

func (a *Armor) NewHTTP() (h *HTTP) {
	e := echo.New()

	a.Echo = e
	h = &HTTP{
		armor:  a,
		echo:   e,
		logger: a.Logger,
	}
	e.HideBanner = true
	e.HidePort = true
	e.Server = &http.Server{
		Addr:         a.Address,
		ReadTimeout:  a.ReadTimeout * time.Second,
		WriteTimeout: a.WriteTimeout * time.Second,
	}
	if a.TLS != nil {
		e.TLSServer = &http.Server{
			Addr:         a.TLS.Address,
			TLSConfig:    a.setupTLSConfig(),
			ReadTimeout:  a.ReadTimeout * time.Second,
			WriteTimeout: a.WriteTimeout * time.Second,
		}
		e.AutoTLSManager.Email = a.TLS.Email
		e.AutoTLSManager.Client = new(acme.Client)
		if a.TLS.DirectoryURL != "" {
			e.AutoTLSManager.Client.DirectoryURL = a.TLS.DirectoryURL
		}

		if a.TLS.KeyPinning {
			a.TLS.pins = newPinning()
			e.Use(a.TLS.pins.process)
		}

	}
	e.Logger = h.logger

	// Internal
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Before(func() {
				c.Response().Header().Set(echo.HeaderServer, "armor/"+Version)
			})
			return next(c)
		}
	})

	return
}

func (h *HTTP) CreateTunnel() {
	c := &tunnel.Configuration{
		Host:       "labstack.me:22",
		Protocol:   "http",
		RemoteHost: "0.0.0.0",
		RemotePort: 80,
		HideBanner: true,
	}
	c.TargetHost, c.TargetPort, _ = tutil.SplitHostPort(h.armor.Address)
	tunnel.Create(c)
}

func (h *HTTP) Start() error {
	a := h.armor
	e := h.echo
	if a.DefaultConfig {
		a.Colorer.Printf("⇨ serving from %s (Local)\n", a.Colorer.Green("http://localhost"+a.Address))
		ip := util.PrivateIP()
		if ip != "" {
			_, port, _ := net.SplitHostPort(a.Address)
			a.Colorer.Printf("⇨ serving from %s (Intranet)\n", a.Colorer.Green(fmt.Sprintf("http://%s:%s", ip, port)))
		}
	} else {
		a.Colorer.Printf("⇨ http server started on %s\n", a.Colorer.Green(a.Address))
	}
	return e.StartServer(e.Server)
}

func (h *HTTP) StartTLS() error {
	a := h.armor
	e := h.echo
	s := e.TLSServer

	// Enable HTTP/2
	s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, "h2")

	if a.TLS.Auto {
		// Enable the "http-01" challenge
		e.Server.Handler = e.AutoTLSManager.HTTPHandler(e.Server.Handler)

		hosts := []string{}
		for host := range a.Hosts {
			hosts = append(hosts, host)
		}
		e.AutoTLSManager.HostPolicy = autocert.HostWhitelist(hosts...) // Added security
		home, err := homedir.Dir()
		if err != nil {
			return err
		}
		if a.TLS.CacheDir == "" {
			a.TLS.CacheDir = filepath.Join(home, ".armor", "cache")
		}
		e.AutoTLSManager.Cache = autocert.DirCache(a.TLS.CacheDir)
	}

	// Load certificates - start
	// Global
	if a.TLS.CertFile != "" && a.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(a.TLS.CertFile, a.TLS.KeyFile)
		if err != nil {
			h.logger.Fatal(err)
		}
		s.TLSConfig.Certificates = append(s.TLSConfig.Certificates, cert)
	}
	// Host
	for _, host := range a.Hosts {
		if host.CertFile == "" || host.KeyFile == "" {
			continue
		}
		cert, err := tls.LoadX509KeyPair(host.CertFile, host.KeyFile)
		if err != nil {
			h.logger.Fatal(err)
		}
		s.TLSConfig.Certificates = append(s.TLSConfig.Certificates, cert)
	}
	s.TLSConfig.BuildNameToCertificate()
	// Load certificates - end

	s.TLSConfig.GetCertificate = func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if cert, ok := s.TLSConfig.NameToCertificate[clientHello.ServerName]; ok {
			// Use provided certificate
			return cert, nil
		} else if a.TLS.Auto {
			cert, err := e.AutoTLSManager.GetCertificate(clientHello)
			if err != nil {
				return nil, err
			}

			if a.TLS.KeyPinning {
				hostPins := h.armor.TLS.pins.pins[clientHello.ServerName]
				if hostPins == nil {
					hostPins = new(pins)
					hostPins.m = make(map[string]struct{})
				}

				for _, crtDer := range cert.Certificate {
					parsedCert, err := x509.ParseCertificate(crtDer)
					if err != nil {
						return nil, err
					}
					pubKeyDer, err := x509.MarshalPKIXPublicKey(parsedCert.PublicKey)
					if err != nil {
						return nil, err
					}
					hash := sha256.Sum256(pubKeyDer)
					keyHashBase := base64.StdEncoding.EncodeToString(hash[:])
					hostPins.m[keyHashBase] = struct{}{}
				}
				h.armor.TLS.pins.mutex.Lock()
				defer h.armor.TLS.pins.mutex.Unlock()
				h.armor.TLS.pins.pins[clientHello.ServerName] = hostPins
			}
			return cert, err
		}
		return nil, nil // No certificate
	}

	a.Colorer.Printf("⇨ https server started on %s\n", a.Colorer.Green(a.TLS.Address))
	return e.StartServer(s)
}
