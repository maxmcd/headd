package tunneld

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestRPC2(t *testing.T) {
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	listener = NewLoggingListener(listener)

	server, err := NewRPC2Server(listener)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = server.ListenAndServe()
	}()

	client := NewRPC2Client(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp4", listener.Addr().String())
	})

	// Check hello
	if err := client.Hello(); err != nil {
		t.Fatal(err)
	}

	appName := "thing"

	// Register our sample app
	appPort, err := client.RegisterApp(App{
		Name:    appName,
		Command: "go",
		Args:    []string{"run", "./sample-app/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if appPort.Port == 0 {
		t.Fatal("Zero value port returned")
	}

	// Wait for it to be healthy
	var healthy bool
	for i := 0; i < 10; i++ {
		resp, err := client.HealthCheckApp(appName)
		if err != nil {
			time.Sleep(time.Millisecond * 200)
			continue
		}
		healthy = resp.Healthy
		if healthy {
			break
		}
	}
	if !healthy {
		t.Fatal("app never returned passing health check")
	}

	if err := client.StopApp(appName); err != nil {
		t.Fatal(err)
	}

	var exited = false
	for i := 0; i < 10; i++ {
		health, _ := client.HealthCheckApp(appName)
		if health != nil && health.Exited {
			exited = true
			break
		}
		time.Sleep(time.Millisecond * 200)
	}
	if !exited {
		t.Fatal("process never exited")
	}

}
