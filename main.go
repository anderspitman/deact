package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-msgauth/dkim"
)

func providers() []string {
	return []string{"fastmail", "gmail"}
}

func main() {

	providerStr := "IMAP Provider. Currently support"
	for _, provider := range providers() {
		providerStr += ", " + provider
	}
	providerStr += "."

	provider := flag.String("provider", "", providerStr)
	username := flag.String("username", "", "IMAP Username")
	password := flag.String("password", "", "IMAP password")
	lastUidArg := flag.Int("last-uid", 0, "Last UID")
	flag.Parse()

	if *username == "" {
		log.Fatal("Missing username")
	}

	if *password == "" {
		log.Fatal("Missing password")
	}

	var imapServer string
	switch *provider {
	case "fastmail":
		imapServer = "imap.fastmail.com:993"
	case "gmail":
		imapServer = "imap.gmail.com:993"
	default:
		log.Fatal("Invalid provider")
	}

	log.Println("Connecting to server...")

	// Connect to server
	c, err := client.DialTLS(imapServer, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Connected")

	// Don't forget to logout
	defer c.Logout()

	// Login
	if err := c.Login(*username, *password); err != nil {
		log.Fatal(err)
	}
	log.Println("Logged in")

	// Select a mailbox
	mbox, err := c.Select("INBOX", false)
	if err != nil {
		//if _, err := c.Select("Sent", false); err != nil {
		log.Fatal(err)
	}

	waitForMsg := func(c *client.Client) []client.Update {

		fmt.Println("wait for msg")

		// Create a channel to receive mailbox updates
		updates := make(chan client.Update, 1)
		c.Updates = updates

		// Start idling
		stopped := false
		stop := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- c.Idle(stop, nil)
		}()

		var out []client.Update

		// Listen for updates
		for {
			select {
			case update := <-updates:
				log.Println("New update:", update)
				if !stopped {
					close(stop)
					stopped = true
					out = append(out, update)
				}
			case err := <-done:
				if err != nil {
					log.Fatal(err)
				}
				log.Println("Not idling anymore")
				return out
			}
		}
	}

	var lastUid uint32 = uint32(*lastUidArg)

	getMessages := func(c *client.Client, mbox *imap.MailboxStatus) uint32 {

		//from := (mbox.Messages - mbox.Recent) + 1
		//to := mbox.Messages

		//fmt.Printf("From %d to %d\n", from, to)

		seqset := new(imap.SeqSet)
		seqset.AddRange(lastUid, 0)
		//items := []imap.FetchItem{imap.FetchEnvelope}

		section := &imap.BodySectionName{}
		items := []imap.FetchItem{imap.FetchUid, section.FetchItem()}

		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)
		go func() {
			done <- c.UidFetch(seqset, items, messages)
		}()

		//log.Printf("Last %d messages:", mbox.Recent)
		var lastUid uint32
		for msg := range messages {
			//log.Println("* " + msg.Envelope.Subject)
			fmt.Println("Message")
			body := msg.GetBody(section)
			if body == nil {
				log.Fatal("Server didn't returned message body")
			}

			lastUid = msg.Uid

			//fmt.Println("body")
			//fmt.Println(body)

			verifications, err := dkim.Verify(body)
			if err != nil {
				log.Fatal(err)
			}

			for _, v := range verifications {
				if v.Err == nil {
					log.Println("Valid signature for:", v.Domain)
				} else {
					log.Println("Invalid signature for:", v.Domain, v.Err)
				}
			}
		}

		if err := <-done; err != nil {
			log.Fatal(err)
		}

		fmt.Println("lastUid", lastUid)
		return lastUid
	}

	processUpdate := func(c *client.Client, update client.Update) {
		switch u := update.(type) {
		case *client.MailboxUpdate:
			fmt.Println("MailboxUpdate")
			printJson(u)

			mbox := u.Mailbox

			lastUid = getMessages(c, mbox)

		case *client.MessageUpdate:
			fmt.Println("MessageUpdate")
			printJson(u)
		case *client.StatusUpdate:
			fmt.Println("StatusUpdate")
			printJson(u)
		case *client.ExpungeUpdate:
			fmt.Println("ExpungeUpdate")
			fmt.Println(u)
		default:
			fmt.Println("Unknown update type")
		}
	}

	lastUid = getMessages(c, mbox)

	for {
		updates := waitForMsg(c)

		for _, u := range updates {
			processUpdate(c, u)
		}
	}
}

func printJson(data interface{}) {
	d, _ := json.MarshalIndent(data, "", "  ")
	fmt.Println(string(d))
}
