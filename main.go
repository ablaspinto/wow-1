package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"time"
)

// Everything ships inside the binary, so deploying is copying one file.
//
//go:embed wines.json templates static
var files embed.FS

func main() {
	data, err := files.ReadFile("wines.json")
	if err != nil {
		log.Fatal(err)
	}
	cat, err := loadCatalog(data)
	if err != nil {
		log.Fatal(err)
	}
	port := env("PORT", "8080")
	// Point IMAGE_BASE_URL at S3/CloudFront later without touching code. SITE_URL is used for links in emails.
	a, err := newApp(cat, env("IMAGE_BASE_URL", "/static/img/"), env("SITE_URL", "http://localhost:"+port))
	if err != nil {
		log.Fatal(err)
	}
	a.ipHeader = os.Getenv("CLIENT_IP_HEADER")
	key, host := os.Getenv("RESEND_API_KEY"), os.Getenv("SMTP_HOST")
	if (key != "" || host != "") && os.Getenv("SITE_URL") == "" {
		log.Fatal("SITE_URL must be set when sending email, or the links in emails would point to localhost")
	}
	switch {
	case key != "":
		a.send, err = resendSender("https://api.resend.com/emails", key, os.Getenv("MAIL_FROM"))
	case host != "":
		a.send, err = smtpSender(host, env("SMTP_PORT", "587"), os.Getenv("SMTP_USER"), os.Getenv("SMTP_PASS"), env("MAIL_FROM", os.Getenv("SMTP_USER")))
	}
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           a.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,  // a slow or silent client can't hold a connection open
		WriteTimeout:      60 * time.Second,  // above the email's worst case (10s connect + 30s SMTP)
		IdleTimeout:       120 * time.Second, // closes idle keep-alive connections
	}
	log.Printf("listening on http://localhost%s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
