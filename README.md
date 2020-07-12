# Reporter

This repository contains a Twilio integration for [porter](https://github.com/ctrezevant/porter), a smart garage door controller.

Configuration options (passed as command line flags):

```
-twsid             Twilio account SID")
-twtoken           Twilio authentication token")
-twsender          Your Twilio sender number")
-recipients        Recipients list in format '+18005550199,+18008675309,...'
-papi              Porter API server URI (default http://localhost:8080)
-pkey              Porter API key
-openthresh        Send notification after this many minutes
-repeatthreshSend  Send repeat notifications at this interval (0 to send only one)
```

This repository contains a Systemd unit file (twporter.service) that can be used to run and manage this service.
