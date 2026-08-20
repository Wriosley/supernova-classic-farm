package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
)

func main() {
	data, _ := os.ReadFile("/tmp/boot.bin")
	msg := &httpv1.ClientBootstrapResponse{}
	if err := proto.Unmarshal(data, msg); err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Printf("AuthBootstrap: %+v\n", msg.GetAuthBootstrap())
	for i, g := range msg.GetGateways() {
		fmt.Printf("Gateway[%d]: id=%q url=%q region=%q priority=%d expires=%d\n",
			i, g.GetGatewayId(), g.GetWebsocketUrl(), g.GetRegion(), g.GetPriority(), g.GetExpiresAtMs())
	}
	fmt.Printf("ServerTimeMs: %d\n", msg.GetServerTimeMs())
}
