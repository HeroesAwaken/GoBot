package main

import (
	"flag"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	log "github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/HeroesAwaken/GoAwaken/core"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
)

func init() {
	flag.StringVar(&configPath, "config", "config.yml", "Path to yml configuration file")
	flag.StringVar(&logLevel, "logLevel", "error", "LogLevel [error|warning|note|debug]")
	flag.Parse()

	log.SetLevel(logLevel)
	MyConfig.Load(configPath)
}

var (
	configPath string
	logLevel   string
	buffer     = make([][]byte, 0)
	prefix     = "!ha"

	// MyConfig Default configuration
	MyConfig = Config{
		MysqlServer: "127.0.0.1:5000",
		MysqlUser:   "",
		MysqlDb:     "",
		MysqlPw:     "",
	}

	Version = "0.0.1"
	mem     runtime.MemStats
	AppName = "AwakenBot"
)

func main() {
	var err error
	if MyConfig.DiscordToken == "" {
		log.Errorln("No token provided. Please run: airhorn -t <bot token>")
		return
	}

	metricConnection := new(core.InfluxDB)
	err = metricConnection.New(MyConfig.InfluxDBHost, MyConfig.InfluxDBDatabase, MyConfig.InfluxDBUser, MyConfig.InfluxDBPassword, AppName, Version)
	if err != nil {
		log.Fatalln("Error connecting to MetricsDB:", err)
	}

	if MyConfig.UseSshTunnel {
		startRemoveCon(MyConfig.SshAddr, MyConfig.LocalAddr, MyConfig.RemoteAddr)
	}

	dbConnection := new(core.DB)
	dbSQL, err := dbConnection.New(MyConfig.MysqlServer, MyConfig.MysqlDb, MyConfig.MysqlUser, MyConfig.MysqlPw)
	if err != nil {
		log.Fatalln("Error connecting to DB:", err)
	}

	err = dbSQL.Ping()
	if err != nil {
		log.Errorln("Error with database: ", err.Error())
		return
	}

	// Create a new Discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + MyConfig.DiscordToken)
	if err != nil {
		log.Errorln("Error creating Discord session: ", err)
		return
	}

	bot := NewAwakenBot(dbSQL, dg, metricConnection)

	// Register ready as a callback for the ready events.
	bot.DG.AddHandler(bot.ready)

	// Register messageCreate as a callback for the messageCreate events.
	bot.DG.AddHandler(bot.messageCreate)

	// Register guildCreate as a callback for the guildCreate events.
	bot.DG.AddHandler(bot.guildCreate)

	// Open the websocket and begin listening.
	err = bot.DG.Open()
	if err != nil {
		log.Errorln("Error opening Discord session: ", err)
		return
	}

	// Wait here until CTRL-C or other term signal is received.
	log.Noteln("HeroesAwakenBot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	bot.DG.Close()
	bot.GetUserRolesByDiscordID.Close()
	dbSQL.Close()
}
