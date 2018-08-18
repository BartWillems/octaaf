package main

import (
	"io/ioutil"

	"octaaf/web"

	log "github.com/sirupsen/logrus"
	"gopkg.in/telegram-bot-api.v4"
)

var settings Settings

const GitUri = "https://gitlab.com/BartWillems/octaaf"

func main() {

	if _, err := settings.Load(); err != nil {
		log.Fatal("Unable to load/parse the settings file: ", err)
	}

	settings.Version = loadVersion()

	if settings.Environment != "production" {
		log.SetLevel(log.DebugLevel)
	}

	initRedis()
	initDB()
	migrateDB()
	initBot()

	go loadReminders()

	cron := initCrons()
	cron.Start()
	defer cron.Stop()

	go func() {
		router := web.New(web.Connections{
			Octaaf:      Octaaf,
			Postgres:    DB,
			Redis:       Redis,
			KaliID:      settings.Telegram.KaliID,
			Environment: settings.Environment,
		})
		err := router.Run()
		if err != nil {
			log.Errorf("Gin creation error: %v", err)
		}
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := Octaaf.GetUpdatesChan(u)

	if err != nil {
		log.Fatalf("Failed to fetch updates: %v", err)
	}

	for update := range updates {

		if update.Message == nil {
			continue
		}

		go handle(update.Message)
	}
}

func loadVersion() string {
	bytes, err := ioutil.ReadFile("assets/version")

	if err != nil {
		log.Errorf("Error while loading version string: %v", err)
		return ""
	}
	log.Infof("Loaded version %v", string(bytes))
	return string(bytes)
}
