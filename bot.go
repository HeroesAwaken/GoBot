package main

import (
	"database/sql"
	"net/http"
	"runtime"
	"strings"
	"time"

	"strconv"

	log "github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/HeroesAwaken/GoAwaken/core"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
)

type AwakenBot struct {
	DB                      *sql.DB
	DG                      *discordgo.Session
	GetUserRolesByDiscordID *sql.Stmt
	GetAllLinkedUsers       *sql.Stmt
	iDB                     *core.InfluxDB
	batchTicker             *time.Ticker
	rolesToIDMap            map[string]map[string]string
	jobsChan                chan botJob
}

type botJob struct {
	jobType string
	data    interface{}
}

// NewAwakenBot creates a new AwakenBot that collects metrics
func NewAwakenBot(db *sql.DB, dg *discordgo.Session, metrics *core.InfluxDB) *AwakenBot {
	var err error

	bot := new(AwakenBot)
	bot.iDB = metrics
	bot.DB = db
	bot.DG = dg

	// store max of 1000 jobs
	bot.jobsChan = make(chan botJob, 1000)

	bot.GetUserRolesByDiscordID, err = bot.DB.Prepare("SELECT roles.slug" +
		"	FROM user_discords" +
		"	LEFT JOIN role_user" +
		"		ON role_user.user_id = user_discords.user_id" +
		"	LEFT JOIN roles" +
		"		ON roles.id = role_user.role_id" +
		"	WHERE discord_id = ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetUserRolesByDiscordID.", err.Error())
	}

	bot.GetAllLinkedUsers, err = bot.DB.Prepare("SELECT user_discords.discord_id, GROUP_CONCAT(roles.slug) as slugs" +
		"	FROM user_discords" +
		"	LEFT JOIN role_user" +
		"		ON role_user.user_id = user_discords.user_id" +
		"	LEFT JOIN roles" +
		"		ON roles.id = role_user.role_id" +
		"	GROUP BY user_discords.id")
	if err != nil {
		log.Fatalln("Could not prepare statement GetAllLinkedUsers.", err.Error())
	}

	bot.CollectGlobalMetrics()
	bot.batchTicker = time.NewTicker(time.Second * 10)
	go func() {
		for range bot.batchTicker.C {
			bot.CollectGlobalMetrics()
		}
	}()

	//Populate rolesToIdMap
	bot.rolesToIDMap = make(map[string]map[string]string)

	// MakaTesting
	bot.rolesToIDMap["320696414483120129"] = make(map[string]string)
	bot.rolesToIDMap["320696414483120129"]["normalUser"] = "337934780463185931"

	// HeroesAwaken
	bot.rolesToIDMap["329078443687936001"] = make(map[string]string)
	bot.rolesToIDMap["329078443687936001"]["awokenlead"] = "329287964544860160"
	bot.rolesToIDMap["329078443687936001"]["awokendev"] = "330164644281057282"
	bot.rolesToIDMap["329078443687936001"]["normalUser"] = "338780084133691392"
	bot.rolesToIDMap["329078443687936001"]["staff"] = "329287195502313475"

	r := mux.NewRouter()
	r.HandleFunc("/api/refresh/{id}", bot.refresh)

	go func() {
		log.Noteln(http.ListenAndServe("0.0.0.0:4000", r))
	}()

	return bot
}

func (bot *AwakenBot) refresh(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	log.Debugln("HTTP request refresh", vars["id"])

	if vars["id"] == "all" {
		bot.jobsChan <- botJob{
			jobType: "refreshAll",
		}
		return
	}

	bot.jobsChan <- botJob{
		jobType: "refresh",
		data:    vars["id"],
	}
}

// CollectGlobalMetrics collects global metrics about the bot and environment
// And sends them to influxdb
func (bot *AwakenBot) CollectGlobalMetrics() {
	runtime.ReadMemStats(&mem)
	tags := map[string]string{"metric": "server_metrics", "server": "global"}
	fields := map[string]interface{}{
		"memAlloc":      int(mem.Alloc),
		"memTotalAlloc": int(mem.TotalAlloc),
		"memHeapAlloc":  int(mem.HeapAlloc),
		"memHeapSys":    int(mem.HeapSys),
	}

	err := bot.iDB.AddMetric("server_metrics", tags, fields)
	if err != nil {
		log.Errorln("Error adding Metric:", err)
	}
}

// This function will be called (due to AddHandler above) when the bot receives
// the "ready" event from Discord.
func (bot *AwakenBot) ready(s *discordgo.Session, event *discordgo.Ready) {

	// Set the playing status.
	s.UpdateStatus(0, prefix+" help")
}

func (bot *AwakenBot) processJobs(s *discordgo.Session, m *discordgo.MessageCreate) {

	// Find the channel that the message came from.
	c, err := s.State.Channel(m.ChannelID)
	if err != nil {
		// Could not find channel.
		return
	}

	// Find the guild for that channel.
	g, err := s.State.Guild(c.GuildID)
	if err != nil {
		// Could not find guild.
		return
	}

	stop := false
	for !stop {
		select {
		case job := <-bot.jobsChan:
			switch job.jobType {
			case "refresh":
				if discordID, ok := job.data.(string); ok {
					bot.refreshUser(discordID, c, g, s)
				}
			case "refreshAll":

				rows, err := bot.GetAllLinkedUsers.Query()
				defer rows.Close()
				if err != nil {
					log.Errorln("Error getting all users.")
				}

				count := 0
				for rows.Next() {
					var discordID, slugs string

					err := rows.Scan(&discordID, &slugs)
					if err != nil {
						log.Errorln("Issue with database:", err.Error())
					}

					slugsSlice := strings.Split(slugs, ",")

					for _, slug := range slugsSlice {
						// Check if we have a matching discord role for the slug
						if roleID, ok := bot.rolesToIDMap[g.ID][slug]; ok {
							log.Debugln("Assigning Role:", g.ID, discordID, roleID)
							s.GuildMemberRoleAdd(g.ID, discordID, roleID)
						}
					}

					count++
				}

				log.Noteln("Updated " + strconv.Itoa(count) + " users.")
			}
		default:
			stop = true
		}
	}
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the autenticated bot has access to.
func (bot *AwakenBot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {

	// Start processing jobs
	go bot.processJobs(s, m)

	// Ignore all messages created by the bot itself
	// This isn't required in this specific example but it's a good practice.
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Find the channel that the message came from.
	c, err := s.State.Channel(m.ChannelID)
	if err != nil {
		// Could not find channel.
		return
	}

	// Find the guild for that channel.
	g, err := s.State.Guild(c.GuildID)
	if err != nil {
		// Could not find guild.
		return
	}

	// check if the message starts with our prefix
	if strings.HasPrefix(m.Content, "!ha ") {

		log.Notef("[%s.%s]: %s > %s", g.Name, c.Name, m.Author.Username, m.Content)

		if c.Name != "bot-spam" && c.Name != "awoken-leads" && c.Name != "mutedisland" {
			// Only respond it specific channels
			return
		}

		args := strings.Split(m.Content, " ")
		if len(args) == 1 {
			// No command given, don't do anything
			return
		}

		command := args[1]
		if len(args) > 2 {
			// Get rid of the prefix + command
			args = args[2:]
		} else {
			// since we don't have any actual arguments except our command, make arguments empty
			args = []string{}
		}

		err = s.MessageReactionAdd(m.ChannelID, m.ID, "✔")
		if err != nil {
			log.Errorln("Error seting Reaction:", err.Error())
		}

		switch command {
		case "help":
			embed := NewEmbed().
				SetTitle("HeroesAwaken").
				SetDescription("Your friendly bot :)").
				AddField(prefix+" help", "Show this lovely help").
				AddField(prefix+" refresh", "Manually refresh your roles").
				SetThumbnail("https://heroesawaken.com/images/logo_new_small.png").
				//SetColor(0x00ff00).
				MessageEmbed
			s.ChannelMessageSendEmbed(m.ChannelID, embed)

		case "refresh":
			err = bot.DB.Ping()
			if err != nil {
				log.Errorln("Error with database: ", err.Error())
				return
			}

			if len(args) == 0 {
				bot.refreshUser(m.Author.ID, c, g, s)
				return
			}
		}
	}
}

func (bot *AwakenBot) refreshUser(discordID string, channel *discordgo.Channel, guild *discordgo.Guild, s *discordgo.Session) {
	log.Debugln("Refreshing discordID", discordID)

	privateChannel, err := s.UserChannelCreate(discordID)
	if err != nil {
		privateChannel = channel
	}

	rows, err := bot.GetUserRolesByDiscordID.Query(discordID)
	defer rows.Close()
	if err != nil {
		s.ChannelMessageSend(privateChannel.ID, "You did not link your discord on the homepage yet.\nHead to https://heroesawaken.com/profile/link/discord to link your Account! :)")
	}

	count := 0
	for rows.Next() {
		var slug string

		err := rows.Scan(&slug)
		if err != nil {
			log.Errorln("Issue with database:", err.Error())
		}

		// Check if we have a matching discord role for the slug
		if roleID, ok := bot.rolesToIDMap[guild.ID][slug]; ok {
			log.Debugln("Assigning Role:", guild.ID, discordID, roleID)
			s.GuildMemberRoleAdd(guild.ID, discordID, roleID)
		}

		count++
	}

	if count == 0 {
		s.ChannelMessageSend(privateChannel.ID, "You did not link your discord on the homepage yet.\nHead to https://heroesawaken.com/profile/link/discord to link your Account! :)")
		return
	}

	s.ChannelMessageSend(privateChannel.ID, "We successfully synced your roles!")
}

// This function will be called (due to AddHandler above) every time a new
// guild is joined.
func (bot *AwakenBot) guildCreate(s *discordgo.Session, event *discordgo.GuildCreate) {
	if event.Guild.Unavailable {
		return
	}
}
