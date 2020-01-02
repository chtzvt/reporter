package porter_twilio

import (
	"flag"
	"fmt"
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
	MsgStateChange int = iota
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

var monitorCtl chan int

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
			os.Exit(0)
		}
	}

}

func statusMonitor() {
	var doors map[string]*DoorWatch
	var errorMsgSent bool

	for {
		select {

		default:
			states, err := porterClient.List()
			if err != nil {
				if !errorMsgSent {
					sendAll(genMsg(MsgMonitorError))
					errorMsgSent = true
				}
				continue
			}

			if errorMsgSent {
				errorMsgSent = false
				sendAll(genMsg(MsgMonitorRecover))
			}

			for doorName, state := range states {
				if _, ok := doors[doorName]; !ok {
					doors[state.Name] = &DoorWatch{
						lastStateChangeTS:    state.LastStateChangeTimestamp,
						lastNotificationSent: time.Time{},
					}
				}

				if state.SensorClosedState == state.State {
					continue
				}

				if time.Since(state.LastStateChangeTimestamp) < openNotificationThreshold {
					continue
				}

				if doors[doorName].lastStateChangeTS == state.LastStateChangeTimestamp && !doors[doorName].lastNotificationSent.IsZero() && time.Since(doors[doorName].lastNotificationSent) < repeatNotificationThreshold {
					continue
				}

				sendAll(genMsg(MsgStateChange, doorName, time.Since(state.LastStateChangeTimestamp)))
				doors[doorName].lastNotificationSent = time.Now()
			}
		}

	}
}

func genMsg(msgType int, values ...interface{}) string {
	const stateStr = "[%v] Porter notice: %s has been open for %v"
	const startStr = "[%v] Porter notice: Door monitor started"
	const stopStr = "[%v] Porter notice: Door monitor is stopping"
	const errorStr = "[%v] Porter notice: I'm having trouble reaching the door controller. The network might be offline, or the controller may need to be rebooted. I won't send any more messages until it comes back online."
	const recoverStr = "[%v] Porter notice: The garage door controller seems to be back online!"

	const notificationTS = "Jan _2 15:04:05" //time.Stamp
	currentTime := time.Now().Format(notificationTS)

	switch msgType {
	case MsgStateChange:
		return fmt.Sprintf(stateStr, currentTime, values[0], values[1])
	case MsgMonitorDying:
		return fmt.Sprintf(stopStr, currentTime)
	case MsgMonitorStarting:
		return fmt.Sprintf(startStr, currentTime)
	case MsgMonitorError:
		return fmt.Sprintf(errorStr, currentTime)
	case MsgMonitorRecover:
		return fmt.Sprintf(recoverStr, currentTime)

	default:
		return ""
	}
}

func sendAll(msg string) {
	wg := &sync.WaitGroup{}
	for _, number := range recipients {
		go (func(wg *sync.WaitGroup, sender, msg string) {
			wg.Add(1)
			sendSMS(sender, number, msg)
			wg.Done()
		})(wg, *sender, msg)
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

	res, _ := httpClient.Do(req)

	return res.StatusCode
}
