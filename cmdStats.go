package main

import (
	"regexp"

	"github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
)

func (bot *AwakenBot) cmdStats(s *discordgo.Session, c *discordgo.Channel, g *discordgo.Guild, m *discordgo.MessageCreate, args []string) {

	// Find the member from the message
	member, err := s.State.Member(g.ID, m.Author.ID)
	if err != nil {
		// Could not find member.
		log.Errorln("Could not find member", err)
		return
	}

	if len(args) == 1 {

		userID, err := bot.getUserID(args[0], s, g)
		if err != nil {
			bot.send(member.User.ID, "Could not detect user. Please try again. "+err.Error(), c, g, s)
			return
		}

		var discordID string
		err = bot.GetIDByHero.QueryRow(userID).Scan(&discordID)
		if err != nil {
			log.Errorln("Could not find discordID")
			bot.send(member.User.ID, "Could not find discordID. "+err.Error(), c, g, s)
		}

		member, err = s.State.Member(g.ID, discordID)
		if err != nil {
			// Could not find member.
			log.Errorln("Could not find member", err)
			return
		}
	}

	heroes := make(map[string]map[string]string)
	rows, err := bot.GetStatsByDiscordID.Query(member.User.ID)
	if err != nil {
		log.Errorln("Failed gettings stats for discord-id "+member.User.ID, err.Error())
	}

	for rows.Next() {
		var heroID, heroName, statsKey, statsValue string
		err := rows.Scan(&heroID, &heroName, &statsKey, &statsValue)
		if err != nil {
			log.Errorln("Issue with database:", err.Error())
		}

		// Check if we already have the hero stats var
		if _, ok := heroes[heroName]; !ok {
			heroes[heroName] = make(map[string]string)
		}

		heroes[heroName][statsKey] = statsValue
	}

	log.Debugln("Got", len(heroes), "heroes for stats", m.ChannelID)

	r, _ := regexp.Compile("([0-9]+).([0-9]+)")

	for heroName, heroStats := range heroes {
		for _, key := range []string{"level", "xp", "games", "win"} {
			if _, ok := heroes[heroName][key]; !ok {
				heroes[heroName][key] = "0"
			}

			// Remove . for values
			match := r.FindStringSubmatch(heroes[heroName][key])
			if len(match) >= 2 {
				heroes[heroName][key] = match[1]
			}
		}

		switch heroes[heroName]["c_team"] {
		case "1":
			heroes[heroName]["c_team"] = "National"
		case "2":
			heroes[heroName]["c_team"] = "Royal"
		}

		switch heroes[heroName]["c_kit"] {
		case "0":
			heroes[heroName]["c_kit"] = "Commando"
		case "1":
			heroes[heroName]["c_kit"] = "Soldier"
		case "2":
			heroes[heroName]["c_kit"] = "Gunner"
		}

		embed := NewEmbed().
			SetTitle("Stats for "+heroName).
			AddField("Team", heroStats["c_team"]).
			AddField("Class", heroStats["c_kit"]).
			AddField("Level", heroStats["level"]).
			AddField("XP", heroStats["xp"]).
			AddField("Games", heroStats["games"]).
			AddField("Wins", heroStats["win"]).
			SetColor(0x00ff00).
			InlineAllFields().
			MessageEmbed
		_, err := s.ChannelMessageSendEmbed(m.ChannelID, embed)
		if err != nil {
			log.Errorln(err)
		}
	}
}
