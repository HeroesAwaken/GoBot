package main

import (
	"database/sql"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"strconv"

	"sync"

	"github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/HeroesAwaken/GoAwaken/core"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
)

type AwakenBot struct {
	DB                           *sql.DB
	DG                           *discordgo.Session
	GetUserRolesByDiscordID      *sql.Stmt
	GetUserRolesByID             *sql.Stmt
	GetAllLinkedUsers            *sql.Stmt
	GetRoleBySlug                *sql.Stmt
	GetStatsByDiscordID          *sql.Stmt
	GetUserWithDiscord           *sql.Stmt
	GetIDByDiscordID             *sql.Stmt
	GetIDByUser                  *sql.Stmt
	GetIDByHero                  *sql.Stmt
	GetDiscordIDByID             *sql.Stmt
	RemoveRoleByID               *sql.Stmt
	iDB                          *core.InfluxDB
	batchTicker                  *time.Ticker
	prefix                       string
	regexUserID                  *regexp.Regexp
	guildMetricsTickers          map[string]*time.Ticker
	rolesToIDMap                 map[string]map[string]string
	jobsChan                     chan botJob
	guildMembers                 map[string]map[string]*discordgo.Member
	guildMembersTemp             map[string]map[string]*discordgo.Member
	guildPresences               map[string]map[string]*discordgo.Presence
	guildMembersMutex            sync.Mutex
	guildPresencesMutex          sync.Mutex
	mapUpdateUsersVariableAmount map[int]*sql.Stmt
}

type botJob struct {
	jobType      string
	data         interface{}
	discordGuild string
}

func (bot *AwakenBot) processJobs(s *discordgo.Session) {
	go func() {
		for {
			select {
			case job := <-bot.jobsChan:
				log.Debugln(len(bot.jobsChan), "Jobs waiting to be processed")
				guild, _ := s.State.Guild(job.discordGuild)

				// Calculate online users
				bot.guildPresencesMutex.Lock()
				bot.guildPresences[guild.ID] = make(map[string]*discordgo.Presence)
				for index := range guild.Presences {
					bot.guildPresences[guild.ID][guild.Presences[index].User.ID] = guild.Presences[index]
				}
				bot.guildPresencesMutex.Unlock()
				//log.Debugln("Online Members:", len(bot.guildPresences[guild.ID]))

				switch job.jobType {
				case "addMembers":
					if members, ok := job.data.([]*discordgo.Member); ok {
						log.Noteln("Adding" + strconv.Itoa(len(members)) + " members to roles.")
						for index := range members {
							memberID := members[index].User.ID
							bot.guildMembersTemp[guild.ID][memberID] = members[index]
						}

						// Chunk lower than 1000 means it's the end
						if len(members) < 1000 {
							bot.guildPresencesMutex.Lock()
							bot.guildMembers[guild.ID] = make(map[string]*discordgo.Member)
							bot.guildMembers[guild.ID] = bot.guildMembersTemp[guild.ID]
							bot.guildPresencesMutex.Unlock()
						}
					}
					log.Noteln("Total Members:", len(bot.guildMembersTemp[guild.ID]))

				case "refresh":
					if discordID, ok := job.data.(string); ok {
						bot.refreshUser(discordID, guild, s)
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
							if roleID, ok := bot.rolesToIDMap[guild.ID][slug]; ok {
								log.Debugln("Assigning Role:", guild.ID, discordID, roleID)
								s.GuildMemberRoleAdd(guild.ID, discordID, roleID)
							}
						}

						count++
					}

					log.Noteln("Updated " + strconv.Itoa(count) + " users.")
				}
			}
		}
	}()
}

// NewAwakenBot creates a new AwakenBot that collects metrics
func NewAwakenBot(db *sql.DB, dg *discordgo.Session, metrics *core.InfluxDB, prefix string) *AwakenBot {
	var err error

	bot := new(AwakenBot)
	bot.iDB = metrics
	bot.DB = db
	bot.DG = dg
	bot.prefix = prefix

	bot.regexUserID, _ = regexp.Compile("<@([0-9]+)>")

	// store max of 1000 jobs
	bot.jobsChan = make(chan botJob, 1000)
	bot.mapUpdateUsersVariableAmount = make(map[int]*sql.Stmt)

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

	bot.GetUserRolesByID, err = bot.DB.Prepare("SELECT roles.slug" +
		"	FROM role_user" +
		"	LEFT JOIN roles" +
		"		ON roles.id = role_user.role_id" +
		"	WHERE role_user.user_id = ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetUserRolesByID.", err.Error())
	}

	bot.GetUserWithDiscord, err = bot.DB.Prepare("SELECT users.id, users.username, users.email, users.birthday, users.ip_address, user_discords.discord_name, user_discords.discord_email, user_discords.discord_discriminator, user_discords.discord_id" +
		"	FROM users" +
		"	LEFT JOIN user_discords" +
		"		ON users.id = user_discords.user_id" +
		"	WHERE users.id = ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetUserWithDiscord.", err.Error())
	}

	bot.GetIDByUser, err = bot.DB.Prepare("SELECT id" +
		"	FROM users" +
		"	WHERE username LIKE ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetIDByUser.", err.Error())
	}

	bot.GetDiscordIDByID, err = bot.DB.Prepare("SELECT discord_id" +
		"	FROM user_discords" +
		"	WHERE user_id LIKE ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetDiscordIDByID.", err.Error())
	}

	bot.RemoveRoleByID, err = bot.DB.Prepare("DELETE role_user" +
		"	FROM role_user" +
		"	WHERE role_user.role_id = ?" +
		"		AND role_user.user_id = ?")
	if err != nil {
		log.Fatalln("Could not prepare statement RemoveRoleByID.", err.Error())
	}

	bot.GetIDByHero, err = bot.DB.Prepare("SELECT user_id" +
		"	FROM game_heroes" +
		"	WHERE heroName LIKE ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetIDByHero.", err.Error())
	}

	bot.GetIDByDiscordID, err = bot.DB.Prepare("SELECT user_id" +
		"	FROM user_discords" +
		"	WHERE discord_id LIKE ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetIDByDiscordID.", err.Error())
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

	bot.GetStatsByDiscordID, err = bot.DB.Prepare(
		"SELECT game_heroes.id, game_heroes.heroName, game_stats.statsKey, game_stats.statsValue" +
			"	FROM user_discords" +
			"	LEFT JOIN game_heroes" +
			"		ON game_heroes.user_id = user_discords.user_id" +
			"	LEFT JOIN game_stats" +
			"		ON game_stats.heroID = game_heroes.id" +
			"	WHERE discord_id = ?")
	if err != nil {
		log.Fatalln("Could not prepare statement GetStatsByDiscordID.", err.Error())
	}

	bot.GetRoleBySlug, err = bot.DB.Prepare("SELECT id, title, slug" +
		"	FROM roles" +
		"	WHERE slug = ?")
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
	bot.guildMembers = make(map[string]map[string]*discordgo.Member)
	bot.guildMembersTemp = make(map[string]map[string]*discordgo.Member)
	bot.guildPresences = make(map[string]map[string]*discordgo.Presence)
	bot.guildMetricsTickers = make(map[string]*time.Ticker)

	// MakaTesting
	bot.rolesToIDMap["320696414483120129"] = make(map[string]string)
	bot.rolesToIDMap["320696414483120129"]["normaluser"] = "337934780463185931"
	bot.rolesToIDMap["320696414483120129"]["awokenlead"] = "337934780463185931"

	// HeroesAwaken
	bot.rolesToIDMap["329078443687936001"] = make(map[string]string)
	bot.rolesToIDMap["329078443687936001"]["awokenlead"] = "329287964544860160"
	bot.rolesToIDMap["329078443687936001"]["awokendev"] = "330164644281057282"
	bot.rolesToIDMap["329078443687936001"]["normaluser"] = "338780084133691392"
	bot.rolesToIDMap["329078443687936001"]["staff"] = "329287195502313475"
	bot.rolesToIDMap["329078443687936001"]["advancedmember"] = "330080920424022017"
	bot.rolesToIDMap["329078443687936001"]["communitymanager"] = "330085415182663682"
	bot.rolesToIDMap["329078443687936001"]["tester"] = "340576558249148430"

	r := mux.NewRouter()
	r.HandleFunc("/api/refresh/{guild}/{id}", bot.refresh)

	go func() {
		log.Noteln(http.ListenAndServe("0.0.0.0:4000", r))
	}()

	// Register ready as a callback for the ready events.
	bot.DG.AddHandler(bot.ready)

	// Register messageCreate as a callback for the messageCreate events.
	bot.DG.AddHandler(bot.messageCreate)

	// Register guildCreate as a callback for the guildCreate events.
	bot.DG.AddHandler(bot.guildCreate)

	bot.DG.AddHandler(bot.memberAdd)
	bot.DG.AddHandler(bot.guildMembersChunk)
	bot.DG.AddHandler(bot.memberRemove)
	bot.DG.AddHandler(bot.memberUpdate)

	return bot
}

func (bot *AwakenBot) refresh(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	log.Debugln("HTTP request refresh", vars["id"])
	log.Debugln("HTTP request refresh", vars["guild"])

	if vars["id"] == "all" {
		bot.jobsChan <- botJob{
			jobType:      "refreshAll",
			discordGuild: vars["guild"],
		}
		return
	}

	bot.jobsChan <- botJob{
		jobType:      "refresh",
		data:         vars["id"],
		discordGuild: vars["guild"],
	}
}

func (bot *AwakenBot) updateUsersByDiscordId(usersAmount int) *sql.Stmt {
	var err error

	// Check if we already have a statement prepared for that amount of stats
	if statement, ok := bot.mapUpdateUsersVariableAmount[usersAmount]; ok {
		return statement
	}

	var query string
	for i := 1; i < usersAmount; i++ {
		query += "?, "
	}

	sql := "INSERT INTO role_user" +
		"	(user_id, role_id)" +
		"	(" +
		"		SELECT user_id, ?" +
		"		FROM user_discords" +
		"		WHERE discord_id IN (" + query + "?)" +
		"	)" +
		"	ON DUPLICATE KEY UPDATE role_id=role_id"

	bot.mapUpdateUsersVariableAmount[usersAmount], err = bot.DB.Prepare(sql)
	if err != nil {
		log.Fatalln("Error preparing stmtGetStatsVariableAmount with "+sql+" query.", err.Error())
	}

	return bot.mapUpdateUsersVariableAmount[usersAmount]

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
	s.UpdateStatus(0, bot.prefix+" help")

	bot.processJobs(s)
}

// This function will be called (due to AddHandler above) when the bot receives
// the "guild_member_add" event from Discord letting us know a new user joined.
func (bot *AwakenBot) memberAdd(s *discordgo.Session, event *discordgo.GuildMemberAdd) {

	log.Noteln("User", event.User.Username, "joined.")

	// Find the guild for that channel.
	g, err := s.State.Guild(event.GuildID)
	if err != nil {
		// Could not find guild.
		return
	}

	member, err := s.State.Member(g.ID, event.User.ID)
	if err != nil {
		log.Errorln("Could not turn joining user to member")
	}

	bot.guildMembersMutex.Lock()
	bot.guildMembers[g.ID][event.User.ID] = member
	bot.guildMembersMutex.Unlock()
	bot.refreshUser(event.User.ID, g, s)
}

func (bot *AwakenBot) memberUpdate(s *discordgo.Session, event *discordgo.GuildMemberUpdate) {
	log.Noteln("User", event.User.Username, "updated.")
	isNewTester := false
	newRoles := event.Roles
	roleExisted := make(map[string]bool)
	oldroles := []string{}

	bot.guildMembersMutex.Lock()
	if bot.guildMembers[event.GuildID][event.User.ID] != nil {
		oldroles = bot.guildMembers[event.GuildID][event.User.ID].Roles
	}
	bot.guildMembers[event.GuildID][event.User.ID] = event.Member
	bot.guildMembersMutex.Unlock()

	for _, oldRole := range oldroles {
		roleExisted[oldRole] = true
	}

	for _, newRole := range newRoles {
		if !roleExisted[newRole] && newRole == bot.rolesToIDMap[event.GuildID]["tester"] {
			isNewTester = true
			break
		}
	}

	if isNewTester {
		var id, title, slug string
		err := bot.GetRoleBySlug.QueryRow("tester").Scan(&id, &title, &slug)
		if err != nil {
			log.Noteln("Could not get role!", err)
			return
		}

		var queryArgs []interface{}
		queryArgs = append(queryArgs, id)
		queryArgs = append(queryArgs, event.User.ID)

		_, err = bot.updateUsersByDiscordId(1).Exec(queryArgs...)
		if err != nil {
			log.Errorln("Failed setting tester role to member ", err.Error())
			return
		}

		// Find the guild for that channel.
		g, err := s.State.Guild(event.GuildID)
		if err != nil {
			// Could not find guild.
			return
		}

		bot.send(event.User.ID, "Gratulations! You are now a Player!\nPlease head over to the #player-changelog channel and read up on how to get started!\n\nSee you on the Battlefield!", nil, g, s)
	}
}

func (bot *AwakenBot) memberRemove(s *discordgo.Session, event *discordgo.GuildMemberRemove) {

	log.Noteln("User", event.User.Username, "left.")

	// Find the guild for that channel.
	g, err := s.State.Guild(event.GuildID)
	if err != nil {
		// Could not find guild.
		return
	}

	bot.guildMembersMutex.Lock()
	delete(bot.guildMembers[g.ID], event.User.ID)
	bot.guildMembersMutex.Unlock()
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the autenticated bot has access to.
func (bot *AwakenBot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {

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

		if c.Name != "bot-spam" && c.Name != "awoken-leads" && c.Name != "mutedisland" && c.Name != "community-manager" && c.Name != "awoken-staff" {
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
				AddField(bot.prefix+" help", "Show this lovely help").
				AddField(bot.prefix+" refresh", "Manually refresh your roles").
				AddField(bot.prefix+" check discord:DISCORDID", "Checks a discord user by his discord ID,Available for CommunityManager+").
				AddField(bot.prefix+" check hero:HERONAME", "Checks a hero and shows his discord ID etc,Available for CommunityManager+").
				AddField(bot.prefix+" check website:WEBSITEUSERNAME", "Checks a user by his website Name,Available for CommunityManager+").
				AddField(bot.prefix+" check @USER", "Checks the tagged discord user,Available for CommunityManager+").
				AddField(bot.prefix+" removePlayer discord:DISCORDID", "Removes the Player role from the ID specified,Available for CommunityManager+").
				AddField(bot.prefix+" removePlayer hero:HERONAME", "Removes the Player role from the Hero specified,Available for CommunityManager+").
				AddField(bot.prefix+" removePlayer website:WEBSITEUSERNAME", "Removes the Player role from the Website username specified,Available for CommunityManager+").
				AddField(bot.prefix+" removePlayer @USER", "Removes the Player role from the User tagged,Available for CommunityManager+").
				AddField(bot.prefix+" stats discord:DISCORDID", "Check stats of the ID specified").
				AddField(bot.prefix+" stats hero:HERONAME", "Check stats of the Hero specified").
				AddField(bot.prefix+" stats website:WEBSITEUSERNAME", "Check stats of the Website username specified").
				AddField(bot.prefix+" stats @USER", "Check stats of the User tagged").
				AddField(bot.prefix+" stats ", "Check your stats").
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
				bot.refreshUserChannel(m.Author.ID, c, g, s)
				return
			}

		case "syncRole":
			bot.cmdSync(s, c, g, m, args)
		case "stats":
			bot.cmdStats(s, c, g, m, args)
		case "check":
			bot.cmdCheck(s, c, g, m, args)
		case "removePlayer":
			bot.cmdRemovePlayer(s, c, g, m, args)
		default:
			bot.send(m.Author.ID, "Unknown function :shrug:", c, g, s)
		}
	}
}

func (bot *AwakenBot) getMembersByRole(roleID string, g *discordgo.Guild) []*discordgo.Member {
	var members []*discordgo.Member

	bot.guildMembersMutex.Lock()
	for index := range bot.guildMembers[g.ID] {
		for _, role := range bot.guildMembers[g.ID][index].Roles {
			if role == roleID {
				members = append(members, bot.guildMembers[g.ID][index])
				break
			}
		}
	}
	bot.guildMembersMutex.Unlock()

	return members
}

func (bot *AwakenBot) send(discordID string, message string, channel *discordgo.Channel, guild *discordgo.Guild, s *discordgo.Session) {
	log.Noteln("Sending '" + message + "' to '" + discordID + "'")
	privateChannel, err := s.UserChannelCreate(discordID)

	if err != nil {
		if channel != nil {
			privateChannel = channel
		} else {
			// can't create private channel and no channel given
			return
		}
	}

	_, err = s.ChannelMessageSend(privateChannel.ID, message)
	if err != nil && channel != nil {
		_, err = s.ChannelMessageSend(channel.ID, message)
		if err != nil {
			log.Errorln(err)
		}
	}
}

func (bot *AwakenBot) refreshUser(discordID string, guild *discordgo.Guild, s *discordgo.Session) {
	bot.refreshUserChannel(discordID, nil, guild, s)
}

func (bot *AwakenBot) refreshUserChannel(discordID string, channel *discordgo.Channel, guild *discordgo.Guild, s *discordgo.Session) {
	log.Debugln("Refreshing discordID", discordID)

	privateChannel, err := s.UserChannelCreate(discordID)

	if err != nil {
		if channel != nil {
			privateChannel = channel
		} else {
			log.Error("Can't create private channel and no channel given")
			// can't create private channel and no channel given
			return
		}
	}

	rows, err := bot.GetUserRolesByDiscordID.Query(discordID)
	defer rows.Close()
	if err != nil {
		_, err := s.ChannelMessageSend(privateChannel.ID, "You did not link your discord on the homepage yet.\nHead to https://heroesawaken.com/profile/link/discord to link your Account! :)")
		if err != nil && channel != nil {
			_, err = s.ChannelMessageSend(channel.ID, "You did not link your discord on the homepage yet.\nHead to https://heroesawaken.com/profile/link/discord to link your Account! :)")
			if err != nil {
				log.Errorln(err)
			}
		}
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
			err = s.GuildMemberRoleAdd(guild.ID, discordID, roleID)
			if err != nil {
				log.Errorln(err)
			}
		}

		count++
	}

	if count == 0 {
		_, err := s.ChannelMessageSend(privateChannel.ID, "You did not link your discord on the homepage yet.\nHead to https://heroesawaken.com/profile/link/discord to link your Account! :)")
		if err != nil && channel != nil {
			_, err = s.ChannelMessageSend(channel.ID, "You did not link your discord on the homepage yet.\nHead to https://heroesawaken.com/profile/link/discord to link your Account! :)")
			if err != nil {
				log.Errorln(err)
			}
		}
		return
	}

	_, err = s.ChannelMessageSend(privateChannel.ID, "We successfully synced your roles!")
	if err != nil && channel != nil {
		_, err = s.ChannelMessageSend(channel.ID, "We successfully synced your roles!")
		if err != nil {
			log.Errorln(err)
		}
	}
}

// This function will be called (due to AddHandler above) every time a new
// guild is joined.
func (bot *AwakenBot) guildCreate(s *discordgo.Session, event *discordgo.GuildCreate) {
	if event.Guild.Unavailable {
		return
	}

	log.Noteln("guild created ", event.Name)

	// Find the guild for that channel.
	g, err := s.State.Guild(event.ID)
	if err != nil {
		// Could not find guild.
		return
	}

	bot.guildMembersMutex.Lock()
	bot.guildMembers[g.ID] = make(map[string]*discordgo.Member)
	bot.guildMembersMutex.Unlock()
	bot.guildMembersTemp[g.ID] = make(map[string]*discordgo.Member)

	bot.getAllMembers(s, g)

	// Collect metrics every 10 seconds
	bot.guildMetricsTickers[g.ID] = time.NewTicker(time.Second * 10)
	go func() {
		for range bot.guildMetricsTickers[g.ID].C {
			bot.metricGuild(s, g)
		}
	}()

	// Refresh all members every 5 minutes
	bot.guildMetricsTickers["refresh:"+g.ID] = time.NewTicker(time.Second * 300)
	go func() {
		for range bot.guildMetricsTickers["refresh:"+g.ID].C {
			bot.guildMembersTemp[g.ID] = make(map[string]*discordgo.Member)
			bot.getAllMembers(s, g)
		}
	}()

}

// Create metrics about a guild
func (bot *AwakenBot) metricGuild(s *discordgo.Session, g *discordgo.Guild) {

	roles := make(map[string]int)
	rolesStruct := make(map[string]*discordgo.Role)

	online := make(map[string]map[string]int)
	online["role"] = make(map[string]int)
	online["status"] = make(map[string]int)
	online["game"] = make(map[string]int)

	bot.guildMembersMutex.Lock()
	for index := range bot.guildMembers[g.ID] {
		for _, role := range bot.guildMembers[g.ID][index].Roles {
			_, ok := rolesStruct[role]

			if !ok {
				dRole, err := s.State.Role(g.ID, role)
				if err != nil {
					log.Errorln("Could not get discord role")
					return
				}

				rolesStruct[role] = dRole
			}

			roles[rolesStruct[role].Name]++
		}
	}
	bot.guildMembersMutex.Unlock()

	bot.guildPresencesMutex.Lock()
	for index := range bot.guildPresences[g.ID] {
		/*
			// Seems to be always empty right now...
			for _, role := range bot.guildPresences[g.ID][index].Roles {
				_, ok := rolesStruct[role]

				if !ok {
					dRole, err := s.State.Role(g.ID, role)
					if err != nil {
						log.Errorln("Could not get discord role")
						return
					}

					rolesStruct[role] = dRole
				}
				log.Noteln(rolesStruct[role].Name)
				online["role"][rolesStruct[role].Name]++
			}
		*/

		online["status"][string(bot.guildPresences[g.ID][index].Status)]++
		if bot.guildPresences[g.ID][index].Game != nil {
			online["game"][bot.guildPresences[g.ID][index].Game.Name]++
		}
	}
	bot.guildPresencesMutex.Unlock()

	tags := map[string]string{"metric": "total_members", "server": g.Name}
	bot.guildMembersMutex.Lock()
	bot.guildPresencesMutex.Lock()
	fields := map[string]interface{}{
		"totalMembers":  len(bot.guildMembers[g.ID]),
		"onlineMembers": len(bot.guildPresences[g.ID]),
	}
	bot.guildPresencesMutex.Unlock()
	bot.guildMembersMutex.Unlock()

	err := bot.iDB.AddMetric("discord_metrics", tags, fields)
	if err != nil {
		log.Errorln("Error adding Metric:", err)
	}

	for roleName := range roles {
		tags := map[string]string{"metric": "role_members", "server": g.Name, "roleName": roleName}
		fields := map[string]interface{}{
			"totalMembers": roles[roleName],
			//"onlineMembers": online["roles"][roleName],
		}

		err := bot.iDB.AddMetric("discord_metrics", tags, fields)
		if err != nil {
			log.Errorln("Error adding Metric:", err)
		}
	}

	for status := range online["status"] {
		tags := map[string]string{"metric": "status_members", "server": g.Name, "status": status}
		fields := map[string]interface{}{
			"onlineMembers": online["status"][status],
		}

		err := bot.iDB.AddMetric("discord_metrics", tags, fields)
		if err != nil {
			log.Errorln("Error adding Metric:", err)
		}
	}

	for game := range online["game"] {
		tags := map[string]string{"metric": "game_members", "server": g.Name, "game": game}
		fields := map[string]interface{}{
			"onlineMembers": online["game"][game],
		}

		err := bot.iDB.AddMetric("discord_metrics", tags, fields)
		if err != nil {
			log.Errorln("Error adding Metric:", err)
		}
	}
}

func (bot *AwakenBot) getAllMembers(s *discordgo.Session, g *discordgo.Guild) {
	err := s.RequestGuildMembers(g.ID, "", 0)
	if err != nil {
		log.Errorln(err)
	}
}

// Create metrics about a guild
func (bot *AwakenBot) guildMembersChunk(s *discordgo.Session, c *discordgo.GuildMembersChunk) {
	log.Noteln(len(c.Members))
	bot.jobsChan <- botJob{
		jobType:      "addMembers",
		data:         c.Members,
		discordGuild: c.GuildID,
	}
}
