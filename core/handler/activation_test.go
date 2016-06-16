package handler

import (
	"sync"
	"testing"

	pb_broker "github.com/TheThingsNetwork/ttn/api/broker"
	pb "github.com/TheThingsNetwork/ttn/api/handler"
	pb_protocol "github.com/TheThingsNetwork/ttn/api/protocol"
	pb_lorawan "github.com/TheThingsNetwork/ttn/api/protocol/lorawan"
	"github.com/TheThingsNetwork/ttn/core/handler/application"
	"github.com/TheThingsNetwork/ttn/core/handler/device"
	"github.com/TheThingsNetwork/ttn/core/types"
	"github.com/TheThingsNetwork/ttn/mqtt"
	"github.com/brocaar/lorawan"
	. "github.com/smartystreets/assertions"
)

func doTestHandleActivation(h *handler, appEUI types.AppEUI, devEUI types.DevEUI, devNonce [2]byte, appKey types.AppKey) (*pb.DeviceActivationResponse, error) {
	devAddr := types.DevAddr{1, 2, 3, 4}

	requestPHY := lorawan.PHYPayload{
		MHDR: lorawan.MHDR{
			MType: lorawan.JoinRequest,
			Major: lorawan.LoRaWANR1,
		},
		MACPayload: &lorawan.JoinRequestPayload{
			AppEUI:   lorawan.EUI64(appEUI),
			DevEUI:   lorawan.EUI64(devEUI),
			DevNonce: devNonce,
		},
	}
	requestPHY.SetMIC(lorawan.AES128Key(appKey))
	requestBytes, _ := requestPHY.MarshalBinary()

	responsePHY := lorawan.PHYPayload{
		MHDR: lorawan.MHDR{
			MType: lorawan.JoinAccept,
			Major: lorawan.LoRaWANR1,
		},
		MACPayload: &lorawan.JoinAcceptPayload{},
	}
	templateBytes, _ := responsePHY.MarshalBinary()
	return h.HandleActivation(&pb_broker.DeduplicatedDeviceActivationRequest{
		Payload: requestBytes,
		AppEui:  &appEUI,
		DevEui:  &devEUI,
		ActivationMetadata: &pb_protocol.ActivationMetadata{Protocol: &pb_protocol.ActivationMetadata_Lorawan{
			Lorawan: &pb_lorawan.ActivationMetadata{
				DevAddr: &devAddr,
			},
		}},
		ResponseTemplate: &pb_broker.DeviceActivationResponse{
			Payload: templateBytes,
		},
	})
}

func TestHandleActivation(t *testing.T) {
	a := New(t)

	h := &handler{
		applications: application.NewApplicationStore(),
		devices:      device.NewDeviceStore(),
	}
	h.mqttActivation = make(chan *mqtt.Activation)
	var wg sync.WaitGroup

	appEUI := types.AppEUI{1, 2, 3, 4, 5, 6, 7, 8}
	devEUI := types.DevEUI{1, 2, 3, 4, 5, 6, 7, 8}
	unknownDevEUI := types.DevEUI{8, 7, 6, 5, 4, 3, 2, 1}

	appKey := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	defaultAppKey := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}

	h.applications.Set(&application.Application{
		AppEUI:        appEUI,
		DefaultAppKey: defaultAppKey,
	})

	h.devices.Set(&device.Device{
		AppEUI: appEUI,
		DevEUI: devEUI,
		AppKey: appKey,
	})

	// Unknown
	res, err := doTestHandleActivation(h,
		appEUI,
		unknownDevEUI,
		[2]byte{1, 2},
		appKey,
	)
	a.So(err, ShouldNotBeNil)
	a.So(res, ShouldBeNil)

	// Wrong AppKey
	res, err = doTestHandleActivation(h,
		appEUI,
		devEUI,
		[2]byte{1, 2},
		[16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	)
	a.So(err, ShouldNotBeNil)
	a.So(res, ShouldBeNil)

	wg.Add(1)
	go func() {
		<-h.mqttActivation
		wg.Done()
	}()

	// Known
	res, err = doTestHandleActivation(h,
		appEUI,
		devEUI,
		[2]byte{1, 2},
		appKey,
	)
	a.So(err, ShouldBeNil)
	a.So(res, ShouldNotBeNil)

	wg.Wait()

	// Same DevNonce used twice
	res, err = doTestHandleActivation(h,
		appEUI,
		devEUI,
		[2]byte{1, 2},
		appKey,
	)
	a.So(err, ShouldNotBeNil)
	a.So(res, ShouldBeNil)

	wg.Add(1)
	go func() {
		<-h.mqttActivation
		wg.Done()
	}()

	// Other DevNonce
	res, err = doTestHandleActivation(h,
		appEUI,
		devEUI,
		[2]byte{2, 1},
		appKey,
	)
	a.So(err, ShouldBeNil)
	a.So(res, ShouldNotBeNil)

	wg.Wait()

	// TODO: Validate response

	// TODO: Check DB

}
