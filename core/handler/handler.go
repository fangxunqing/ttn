package handler

import (
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/TheThingsNetwork/ttn/api"
	pb_broker "github.com/TheThingsNetwork/ttn/api/broker"
	pb "github.com/TheThingsNetwork/ttn/api/handler"
	"github.com/TheThingsNetwork/ttn/core"
	"github.com/TheThingsNetwork/ttn/core/handler/application"
	"github.com/TheThingsNetwork/ttn/core/handler/device"
	"github.com/TheThingsNetwork/ttn/mqtt"
	"gopkg.in/redis.v3"
)

// Handler component
type Handler interface {
	core.ComponentInterface

	HandleUplink(uplink *pb_broker.DeduplicatedUplinkMessage) error
	HandleActivation(activation *pb_broker.DeduplicatedDeviceActivationRequest) (*pb.DeviceActivationResponse, error)
	EnqueueDownlink(appDownlink *mqtt.DownlinkMessage) error
}

// NewRedisHandler creates a new Redis-backed Handler
func NewRedisHandler(client *redis.Client, ttnBrokerAddr string, mqttUsername string, mqttPassword string, mqttBrokers ...string) Handler {
	return &handler{
		devices:       device.NewRedisDeviceStore(client),
		applications:  application.NewRedisApplicationStore(client),
		ttnBrokerAddr: ttnBrokerAddr,
		mqttUsername:  mqttUsername,
		mqttPassword:  mqttPassword,
		mqttBrokers:   mqttBrokers,
	}
}

type handler struct {
	*core.Component

	devices      device.Store
	applications application.Store

	ttnBrokerAddr string
	ttnBrokerConn *grpc.ClientConn
	ttnBroker     pb_broker.BrokerClient

	downlink chan *pb_broker.DownlinkMessage

	mqttClient   mqtt.Client
	mqttUsername string
	mqttPassword string
	mqttBrokers  []string

	mqttUp         chan *mqtt.UplinkMessage
	mqttActivation chan *mqtt.Activation
}

func (h *handler) Init(c *core.Component) error {
	h.Component = c
	err := h.Component.UpdateTokenKey()
	if err != nil {
		return err
	}
	err = h.Component.Announce()
	if err != nil {
		return err
	}

	var brokers []string
	for _, broker := range h.mqttBrokers {
		brokers = append(brokers, fmt.Sprintf("tcp://%s", broker))
	}

	err = h.HandleMQTT(h.mqttUsername, h.mqttPassword, brokers...)
	if err != nil {
		return err
	}

	err = h.associateBroker()
	if err != nil {
		return err
	}

	return nil
}

func (h *handler) associateBroker() error {
	conn, err := grpc.Dial(h.ttnBrokerAddr, api.DialOptions...)
	if err != nil {
		return err
	}
	h.ttnBrokerConn = conn
	h.ttnBroker = pb_broker.NewBrokerClient(conn)

	ctx := h.GetContext()

	upStream, err := h.ttnBroker.Subscribe(ctx, &pb_broker.SubscribeRequest{})
	if err != nil {
		return err
	}

	h.downlink = make(chan *pb_broker.DownlinkMessage)
	downStream, err := h.ttnBroker.Publish(ctx)
	if err != nil {
		return err
	}

	go func() {
		for {
			in, err := upStream.Recv()
			if err != nil && (err == io.EOF ||
				grpc.Code(err) == codes.Canceled ||
				grpc.Code(err) == codes.Internal ||
				grpc.Code(err) == codes.Unauthenticated) {
				h.Ctx.Fatalf("ttn/handler: Stopping Broker subscribe: %s", err) // TODO: Restart
				break
			}
			if err != nil {
				h.Ctx.Warnf("ttn/handler: Error in Broker subscribe: %s", err)
				<-time.After(api.Backoff)
				continue
			}
			go h.HandleUplink(in)
		}
	}()

	go func() {
		for downlink := range h.downlink {
			err := downStream.Send(downlink)
			if err != nil && (err == io.EOF ||
				grpc.Code(err) == codes.Canceled ||
				grpc.Code(err) == codes.Internal ||
				grpc.Code(err) == codes.Unauthenticated) {
				h.Ctx.Fatalf("ttn/handler: Stopping Broker publish: %s", err) // TODO: Restart
				break
			}
			if err != nil {
				h.Ctx.Warnf("ttn/handler: Error in Broker publish: %s", err)
				<-time.After(api.Backoff)
				continue
			}
		}
	}()

	return nil
}
