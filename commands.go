package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"octaaf/models"
	"octaaf/scrapers"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	humanize "github.com/dustin/go-humanize"
	"github.com/go-redis/cache"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
	opentracing "github.com/opentracing/opentracing-go"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

func changelog(message *OctaafMessage) error {
	if settings.Version == "" {
		return message.Reply("Current version not found, check the changelog here: " + GitUri + "/tags")
	}

	return message.Reply(fmt.Sprintf("%v/tags/%v", GitUri, settings.Version))
}

func all(message *OctaafMessage) error {
	userIDSpan := message.Span.Tracer().StartSpan(
		"Fetch user ids from redis",
		opentracing.ChildOf(message.Span.Context()),
	)
	members := Redis.SMembers(fmt.Sprintf("members_%v", message.Chat.ID)).Val()

	userIDSpan.Finish()

	if len(members) == 0 {
		return message.Reply("I'm afraid I can't do that.")
	}

	usernamesSpan := message.Span.Tracer().StartSpan(
		"Load usernames",
		opentracing.ChildOf(message.Span.Context()),
	)

	// used to load the usernames in goroutines
	var wg sync.WaitGroup
	var response string
	// Get the members' usernames
	for _, member := range members {
		memberID, err := strconv.Atoi(member)

		if err != nil {
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			username, err := getUserName(memberID, message.Chat.ID)
			if err == nil {
				response += fmt.Sprintf("@%v ", username)
			}
		}()
	}

	wg.Wait()
	usernamesSpan.Finish()
	return message.Reply(fmt.Sprintf("%v %v", response, message.CommandArguments()))
}

func remind(message *OctaafMessage) error {
	w := when.New(nil)
	w.Add(en.All...)
	w.Add(common.All...)

	r, err := w.Parse(message.CommandArguments(), time.Now())

	if err != nil {
		log.Errorf("Reminder parser error: %v", err)
		message.Span.SetTag("error", err)
		return message.Reply("Unable to parse")
	}

	if r == nil {
		log.Error("No reminder found for message: ", message.CommandArguments())
		message.Span.SetTag("error", "No reminder found")
		return message.Reply("No reminder found")
	}

	reminder := models.Reminder{
		ChatID:    message.Chat.ID,
		UserID:    message.From.ID,
		MessageID: message.MessageID,
		Message:   message.CommandArguments(),
		Deadline:  r.Time,
		Executed:  false}

	go startReminder(reminder)
	return message.Reply("Reminder saved!")
}

func sendRoll(message *OctaafMessage) error {
	rand.Seed(time.Now().UnixNano())
	roll := strconv.Itoa(rand.Intn(9999999999-1e9) + 1e9)
	points := [9]string{"👌 Dubs", "🙈 Trips", "😱 Quads", "🤣😂 Penta", "👌👌🤔🤔😂😂 Hexa", "🙊🙉🙈🐵 Septa", "🅱️Octa", "💯💯💯 El Niño"}
	var dubscount int8 = -1

	for i := len(roll) - 1; i > 0; i-- {
		if roll[i] == roll[i-1] {
			dubscount++
		} else {
			break
		}
	}

	if dubscount > -1 {
		roll = points[dubscount] + " " + roll
	}
	return message.Reply(roll)
}

func count(message *OctaafMessage) error {
	return message.Reply(fmt.Sprintf("%v", message.MessageID))
}

func whoami(message *OctaafMessage) error {
	return message.Reply(fmt.Sprintf("%v", message.From.ID))
}

func m8Ball(message *OctaafMessage) error {

	if len(message.CommandArguments()) == 0 {
		return message.Reply("Oi! You have to ask question hé 🖕")
	}

	answers := [20]string{"👌 It is certain",
		"👌 It is decidedly so",
		"👌 Without a doubt",
		"👌 Yes definitely",
		"👌 You may rely on it",
		"👌 As I see it, yes",
		"👌 Most likely",
		"👌 Outlook good",
		"👌 Yes",
		"👌 Signs point to yes",
		"☝ Reply hazy try again",
		"☝ Ask again later",
		"☝ Better not tell you now",
		"☝ Cannot predict now",
		"☝ Concentrate and ask again",
		"🖕 Don't count on it",
		"🖕 My reply is no",
		"🖕 My sources say no",
		"🖕 Outlook not so good",
		"🖕 Very doubtful"}
	rand.Seed(time.Now().UnixNano())
	roll := rand.Intn(19)
	return message.Reply(answers[roll])
}

func sendBodegem(message *OctaafMessage) error {
	span := message.Span.Tracer().StartSpan(
		"Fetch Bodegem Map",
		opentracing.ChildOf(message.Span.Context()),
	)
	msg := tgbotapi.NewLocation(message.Chat.ID, 50.8614773, 4.211304)
	span.Finish()
	msg.ReplyToMessageID = message.MessageID
	_, err := Octaaf.Send(msg)
	return err
}

func where(message *OctaafMessage) error {
	argument := strings.Replace(message.CommandArguments(), " ", "+", -1)

	span := message.Span.Tracer().StartSpan(
		"Fetch location",
		opentracing.ChildOf(message.Span.Context()),
	)
	location, found := scrapers.GetLocation(argument, settings.Google.ApiKey)
	span.Finish()

	if !found {
		return message.Reply("This place does not exist 🙈🙈🙈🤔🤔�")
	}

	msg := tgbotapi.NewLocation(message.Chat.ID, location.Lat, location.Lng)
	msg.ReplyToMessageID = message.MessageID
	_, err := Octaaf.Send(msg)
	return err
}

func what(message *OctaafMessage) error {
	span := message.Span.Tracer().StartSpan(
		"Trying to explain something...",
		opentracing.ChildOf(message.Span.Context()),
	)
	query := message.CommandArguments()
	result, found, err := scrapers.What(query)
	span.Finish()

	if err != nil {
		log.Errorf("Unable to explain '%v'. Error: %v", query, err)
		span.SetTag("error", err)
		return message.Reply("I do not know, something went bork...")
	}

	if !found {
		return message.Reply("That is forbidden knowledge.")
	}

	return message.Reply(fmt.Sprintf("%v: %v", Markdown(query, mdbold), result))
}

func weather(message *OctaafMessage) error {
	weatherSpan := message.Span.Tracer().StartSpan(
		"Fetching weather status...",
		opentracing.ChildOf(message.Span.Context()),
	)
	weather, found := scrapers.GetWeatherStatus(message.CommandArguments(), settings.Google.ApiKey)

	weatherSpan.SetTag("found", found == true)
	weatherSpan.Finish()
	if !found {
		return message.Reply("No data found 🙈🙈🙈")
	}
	return message.Reply("*Weather:* " + weather)
}

func search(message *OctaafMessage) error {
	if len(message.CommandArguments()) == 0 {
		return message.Reply("What do you expect me to do? 🤔🤔🤔🤔")
	}

	searchSpan := message.Span.Tracer().StartSpan(
		"Searching on duckduckgo...",
		opentracing.ChildOf(message.Span.Context()),
	)

	url, found := scrapers.Search(message.CommandArguments(), message.Command() == "search_nsfw")

	searchSpan.SetTag("found", found == true)
	searchSpan.Finish()

	if found {
		return message.Reply(MDEscape(url))
	}
	return message.Reply("I found nothing 😱😱😱")
}

func sendStallman(message *OctaafMessage) error {
	fetchSpan := message.Span.Tracer().StartSpan(
		"Fetch a stallman",
		opentracing.ChildOf(message.Span.Context()),
	)

	image, err := scrapers.GetStallman()

	fetchSpan.Finish()

	if err != nil {
		fetchSpan.SetTag("error", err)
		return message.Reply("Stallman went bork?")
	}
	return message.Reply(image)
}

func sendImage(message *OctaafMessage) error {
	var images []string
	var err error
	more := message.Command() == "more"
	message.Span.SetTag("more", more)
	key := fmt.Sprintf("images_%v", message.Chat.ID)
	if !more {
		if len(message.CommandArguments()) == 0 {
			return message.Reply(fmt.Sprintf("What am I to do, @%v? 🤔🤔🤔🤔", message.From.UserName))
		}

		fetchSpan := message.Span.Tracer().StartSpan(
			"fetch query from google",
			opentracing.ChildOf(message.Span.Context()),
		)

		images, err = scrapers.GetImages(message.CommandArguments(), message.Command() == "img_sfw")

		fetchSpan.Finish()

		if err != nil {
			fetchSpan.SetTag("error", err)
			return message.Reply("Something went wrong!")
		}

		Cache.Set(&cache.Item{
			Key:        key,
			Object:     images,
			Expiration: 0,
		})
	} else {
		if err := Cache.Get(key, &images); err != nil {
			return message.Reply("I can't fetch them for you right now.")
		}

		// Randomly order images for a different /more
		for i := range images {
			j := rand.Intn(i + 1)
			images[i], images[j] = images[j], images[i]
		}
	}

	timeout := time.Duration(2 * time.Second)
	client := &http.Client{
		Timeout: timeout,
	}

	for attempt, url := range images {

		imgSpan := message.Span.Tracer().StartSpan(
			fmt.Sprintf("Download attempt %v", attempt),
			opentracing.ChildOf(message.Span.Context()),
		)

		imgSpan.SetTag("url", url)
		imgSpan.SetTag("attempt", attempt)

		res, err := client.Get(url)

		imgSpan.Finish()

		if err != nil {
			imgSpan.SetTag("error", err)
			continue
		}

		defer res.Body.Close()

		img, err := ioutil.ReadAll(res.Body)

		if err != nil {
			imgSpan.SetTag("error", err)
			log.Errorf("Unable to load image %v; error: %v", url, err)
			continue
		}

		err = message.Reply(img)

		if err == nil {
			return nil
		}
		imgSpan.SetTag("error", err)
	}

	return message.Reply("I did not find images for the query: `" + message.CommandArguments() + "`")
}

func xkcd(message *OctaafMessage) error {
	image, err := scrapers.GetXKCD()

	if err != nil {
		return message.Reply("Failed to parse XKCD image")
	}

	return message.Reply(image)
}

func doubt(message *OctaafMessage) error {
	return message.Reply(assets.Doubt)
}

func quote(message *OctaafMessage) error {
	quoteSpan := message.Span.Tracer().StartSpan(
		"quote",
		opentracing.ChildOf(message.Span.Context()),
	)
	// Fetch a random quote
	if message.ReplyToMessage == nil {
		quoteSpan.SetOperationName("Loading quote...")
		quote := models.Quote{}

		err := quote.Search(DB, message.Chat.ID, message.CommandArguments())

		quoteSpan.Finish()

		if err != nil {
			log.Errorf("Quote fetch error: %v", err)
			quoteSpan.SetTag("error", err)
			return message.Reply("No quote found boi")
		}

		userSpan := message.Span.Tracer().StartSpan(
			"Loading username...",
			opentracing.ChildOf(message.Span.Context()),
		)
		username, userErr := getUserName(quote.UserID, message.Chat.ID)

		userSpan.Finish()

		if userErr != nil {
			log.Errorf("Unable to find the username for id '%v' : %v", quote.UserID, userErr)
			userSpan.SetTag("error", userErr)
			return message.Reply(quote.Quote)
		}
		msg := fmt.Sprintf("\"%v\"", Markdown(quote.Quote, mdquote))
		msg += fmt.Sprintf(" \n    ~@%v", username)
		return message.Reply(msg)
	}

	quoteSpan.SetOperationName("Saving quote...")

	// Unable to store this quote
	if message.ReplyToMessage.Text == "" {
		quoteSpan.SetTag("error", "No quote found")
		quoteSpan.Finish()
		return message.Reply("No text found in the comment. Not saving the quote!")
	}

	err := DB.Save(&models.Quote{
		Quote:  message.ReplyToMessage.Text,
		UserID: message.ReplyToMessage.From.ID,
		ChatID: message.Chat.ID})

	quoteSpan.Finish()

	if err != nil {
		log.Errorf("Unable to save quote '%v', error: %v", message.ReplyToMessage.Text, err)
		quoteSpan.SetTag("error", err)
		return message.Reply("Unable to save the quote...")
	}

	return message.Reply("Quote successfully saved!")
}

func nextLaunch(message *OctaafMessage) error {
	fetchSpan := message.Span.Tracer().StartSpan(
		"Fetching nextlaunch data...",
		opentracing.ChildOf(message.Span.Context()),
	)
	res, err := http.Get("https://launchlibrary.net/1.3/launch?next=5&mode=verbose")

	fetchSpan.Finish()

	if err != nil {
		fetchSpan.SetTag("error", err)
		return message.Reply("Unable to fetch launch data")
	}

	defer res.Body.Close()

	launchJSON, err := ioutil.ReadAll(res.Body)

	if err != nil {
		return message.Reply("Unable to fetch launch data")
	}

	launches := gjson.Get(string(launchJSON), "launches").Array()

	var msg = "*Next 5 launches:*"

	layout := "January 2, 2006 15:04:05 MST"

	for index, launch := range launches {
		whenStr := launch.Get("net").String()
		when, err := time.Parse(layout, whenStr)

		msg += fmt.Sprintf("\n*%v*: %v", index+1, MDEscape(launch.Get("name").String()))

		if err != nil {
			msg += fmt.Sprintf("\n	  %v", Markdown(whenStr, mdcursive))
		} else {
			msg += fmt.Sprintf("\n	  %v", Markdown(humanize.Time(when), mdcursive))
		}

		vods := launch.Get("vidURLs").Array()

		if len(vods) > 0 {
			msg += "\n    " + MDEscape(vods[0].String())
		}
	}

	return message.Reply(msg)
}

func kaliRank(message *OctaafMessage) error {
	if message.Chat.ID != settings.Telegram.KaliID {
		return message.Reply("You are not allowed!")
	}

	kaliRank := []models.MessageCount{}
	err := DB.Order("diff DESC").Limit(5).All(&kaliRank)

	if err != nil {
		log.Error("Unable to fetch kali rankings: ", err)
		return message.Reply("Unable to fetch the kali rankings")
	}

	var msg = "*Kali rankings:*"
	for index, rank := range kaliRank {
		msg += fmt.Sprintf("\n`#%v:` *%v messages*   _~%v_", index+1, rank.Diff, rank.CreatedAt.Format("Monday, 2 January 2006"))
	}

	return message.Reply(msg)
}

func iasip(message *OctaafMessage) error {
	server := "http://159.89.14.97:6969"

	res, err := http.Get(server)
	if err != nil {
		log.Error("Unable to fetch IASIP quote: ", err)
		return message.Reply("Unable to fetch iasip quote...you goddamn bitch you..")
	}

	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Error("Unable to fetch IASIP quote: ", err)
		return message.Reply("Unable to fetch iasip quote...you goddamn bitch you..")
	}

	return message.Reply(string(body))
}

func care(message *OctaafMessage) error {
	msg := "¯\\_(ツ)_/¯"

	reply := message.ReplyToMessage
	if reply == nil {
		return message.Reply(MDEscape(msg))
	}

	return message.ReplyTo(MDEscape(msg), reply.MessageID)
}

func pollentiek(message *OctaafMessage) error {
	orientations := map[string][]string{
		"corrupte sos": []string{
			"Liever poen dan groen! 🤑🤑",
			"Zwijg bruine rakker!! 🤚🤚",
			"Wij staken voor uw toekomst 😴😴🍻🍻🍻",
			"Sommige mensen denken dat ze kost wat kost mogen gaan werken 🤓🤓🤓",
		},
		"karakterloze tsjeef": []string{
			"Eat, sleep, tsjeef, repeat 💅💅💅",
			"Is hier nog ergens een chassidische jood beschikbaar om op te komen voor mij? Aub ik smeek u Bartje maakt mij kapot.. 🕎🕎",
			"🆘🆘🆘 't Is al de schuld van de sossen! 🆘🆘🆘",
			"Ik heb geen probleem met moslims in de straat, maar ...💁💁💁",
		},
		"racistische marginale zot": []string{
			"🆘🆘🆘 't Is al de schuld van de sossen! 🆘🆘🆘",
			"Komt door al die vluchtelingen 🏃🏃🏃🏃",
			"Dit is fake nieuws. U kan die posts gewoon op internet vinden. Of zelf maken.\nIemand heeft mijn profielfoto en voornaam gestolen en post zo'n uitspraken in mijn naam.\nMaar die zijn niet van mij. 🤷‍♂️🤷‍♂️🤷‍♂️",
			"😤😤😤Het wordt hoog tijd dat de mensch terug zijn schild en zijn vriend draagtdt!!😤😤😤",
			"Moest Vlaams Belang meer zetels hebben zou dit niet gebeuren punt 🛋🛋🛋🛋",
			"😈Obama😈 and 😈Hillary😈 both smell like 🔥sulfur🔥.",
			"Goddamn liberals 😤😤😤",
			"Beter dood dan rood!🔴☠️🔴☠️🔴☠️",
			"Linkse ratten!! Rolt uw matten!!🐀🐀🐀",
			"Het is weer nen makaak ze 🙉🙉🙉",
			"'t Zijn altijd dezelfden!! 😒😒😒😒",
		},
		"gierige lafaard met geld": []string{
			"🆘🆘🆘 't Is al de schuld van de sossen! 🆘🆘🆘",
			"🇩🇪🇩🇪🇩🇪WIR SCHAFFEN DAS🇩🇪🇩🇪🇩🇪",
			"WIR HABEN DAS NICHT GEWURST🚿🚿🚿",
			"Gewoon doen, watermeloen 🍉🍉🍉🤤🤤",
			"Ge zijt ne flipflop! U en uw partij!🤔🤔🤔🤔",
			"🤤🤤🤤Here is how Bernie can still win..🤤🤤🤤",
		},
	}

	keys := reflect.ValueOf(orientations).MapKeys()

	rand.Seed(time.Now().UnixNano())
	orientation := keys[rand.Intn(len(keys))].String()

	msg := fmt.Sprintf("You are a fullblooded %v.\n", Markdown(orientation, mdbold))

	rand.Seed(time.Now().UnixNano())
	randomSayIndex := rand.Intn(len(orientations[orientation]))
	saying := orientations[orientation][randomSayIndex]

	msg += fmt.Sprintf("Don't forget to remind everyone around you by proclaiming at least once a day:\n\n%s", Markdown(saying, mdbold))

	return message.Reply(msg)
}
