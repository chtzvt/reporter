package main

import (
	"flag"
	"fmt"
	"github.com/hako/durafmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"porter/client"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	MsgStateChangeOpen int = iota
	MsgStateChangeClosed
	MsgMonitorDying
	MsgMonitorStarting
	MsgMonitorError
	MsgMonitorRecover
)

type DoorWatch struct {
	lastStateChangeTS    time.Time
	lastNotificationSent time.Time
}

var porterClient *client.Client

var accountSID, twilioAuthToken, sender *string
var recipients []string

var repeatNotificationThreshold, openNotificationThreshold time.Duration

func main() {
	accountSID = flag.String("twsid", "", "Twilio account SID")
	twilioAuthToken = flag.String("twtoken", "", "Twilio authentication token")
	sender = flag.String("twsender", "", "Your Twilio sender number")
	rcptList := flag.String("recipients", "", "Recipients list in format '+18005550199,+18008675309,...'")

	porterApiURI := flag.String("papi", "http://localhost:8080", "Porter API server URI")
	porterApiKey := flag.String("pkey", "default", "Porter API key")

	openTime := flag.Int("openthresh", 30, "Send notification after this many minutes")
	notifyTime := flag.Int("repeatthresh", 60, "Send repeat notifications at this interval (0 to send only one)")

	flag.Parse()

	if *accountSID == "" || *twilioAuthToken == "" || *sender == "" || *rcptList == "" || *porterApiKey == "" || *porterApiURI == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	porterClient = client.NewClient()
	porterClient.APIKey = *porterApiKey
	porterClient.HostURI = *porterApiURI

	openNotificationThreshold = time.Duration(*openTime) * time.Minute
	repeatNotificationThreshold = time.Duration(*notifyTime) * time.Minute

	recipients = strings.Split(*rcptList, ",")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	signal.Notify(sig, os.Kill)
	signal.Notify(sig, syscall.SIGTERM)

	sendAll(genMsg(MsgMonitorStarting))
	go statusMonitor()

	for {
		select {
		case <-sig:
			fmt.Printf("%v Porter Twilio: Stopping daemon...\n", time.Now())
			sendAll(genMsg(MsgMonitorDying))
			time.Sleep(3 * time.Second)
			os.Exit(0)
		}
	}

}

func statusMonitor() {
	doors := make(map[string]*DoorWatch)
	var errorMsgSent bool

	for {
		select {

		default:
			time.Sleep(5 * time.Second)

			states, err := porterClient.List()
			if err != nil {
				if !errorMsgSent {
					errorMsgSent = true
					sendAll(genMsg(MsgMonitorError))
				}
				continue
			}

			if errorMsgSent {
				errorMsgSent = false
				sendAll(genMsg(MsgMonitorRecover))
			}

			for doorName, state := range states {
				if _, ok := doors[doorName]; !ok {
					doors[doorName] = &DoorWatch{
						lastStateChangeTS:    state.LastStateChangeTimestamp,
						lastNotificationSent: time.Time{},
					}
				}

				if state.SensorClosedState == state.State {
					if doors[doorName].lastStateChangeTS != state.LastStateChangeTimestamp && !doors[doorName].lastNotificationSent.IsZero() {
						delete(doors, doorName)
						sendAll(genMsg(MsgStateChangeClosed, doorName, time.Since(state.LastStateChangeTimestamp)))
					}
					continue
				}

				if time.Since(state.LastStateChangeTimestamp) < openNotificationThreshold {
					continue
				}

				if doors[doorName].lastStateChangeTS == state.LastStateChangeTimestamp && !doors[doorName].lastNotificationSent.IsZero() {
					if time.Since(doors[doorName].lastNotificationSent) < repeatNotificationThreshold {
						continue
					}
				}

				doors[doorName].lastNotificationSent = time.Now()
				doors[doorName].lastStateChangeTS = state.LastStateChangeTimestamp

				sendAll(genMsg(MsgStateChangeOpen, doorName, time.Since(state.LastStateChangeTimestamp)))
			}
		}

	}
}

func genMsg(msgType int, values ...interface{}) string {
	const openStateStr = "[%v] Porter notice: %s has been open for %v."
	const closedStateStr = "[%v] Porter notice: %s is now closed."
	const startStr = "[%v] Porter notice: Door monitor started."
	const stopStr = "[%v] Porter notice: Door monitor is stopping."
	const errorStr = "[%v] Porter notice: I'm having trouble reaching the door controller. The network might be offline, or the controller may need to be rebooted. I won't send any more messages until I can reach it."
	const recoverStr = "[%v] Porter notice: The garage door controller is back online! Status updates will resume."

	currentTime := time.Now()
	timeStr := currentTime.Format("Mon Jan 2 '06 3:4 PM")

	switch msgType {
	case MsgStateChangeOpen:
		return fmt.Sprintf(openStateStr, timeStr, values[0], durafmt.ParseShort(values[1].(time.Duration)).String())
	case MsgStateChangeClosed:
		return fmt.Sprintf(closedStateStr, timeStr, values[0])
	case MsgMonitorDying:
		return fmt.Sprintf(stopStr, timeStr)
	case MsgMonitorStarting:
		return fmt.Sprintf(startStr, timeStr)
	case MsgMonitorError:
		return fmt.Sprintf(errorStr, timeStr)
	case MsgMonitorRecover:
		return fmt.Sprintf(recoverStr, timeStr)

	default:
		return ""
	}
}

func sendAll(msg string) {
	wg := &sync.WaitGroup{}
	for _, number := range recipients {
		go (func(wg *sync.WaitGroup, from, to string) {
			wg.Add(1)
			sendSMS(from, to, msg)
			wg.Done()
		})(wg, *sender, number)
	}
	wg.Wait()
}

func sendSMS(sender, recipient, message string) int {
	httpClient := &http.Client{}
	httpClient.Timeout = 30 * time.Second

	apiUrl := strings.Join([]string{"https://api.twilio.com/2010-04-01/Accounts/", *accountSID, "/Messages.json"}, "")

	v := url.Values{}
	v.Set("To", recipient)
	v.Set("From", sender)
	v.Set("Body", message)
	payload := *strings.NewReader(v.Encode())

	req, _ := http.NewRequest("POST", apiUrl, &payload)

	req.SetBasicAuth(*accountSID, *twilioAuthToken)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	if res, err := httpClient.Do(req); err != nil {
		return -1
	} else {
		return res.StatusCode
	}
}
