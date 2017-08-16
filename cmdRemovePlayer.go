package main

import (
	"github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
)

func (bot *AwakenBot) cmdRemovePlayer(s *discordgo.Session, c *discordgo.Channel, g *discordgo.Guild, m *discordgo.MessageCreate, args []string) {

	// Find the member from the message
	member, err := s.State.Member(g.ID, m.Author.ID)
	if err != nil {
		// Could not find member.
		log.Errorln("Could not find member", err)
		return
	}

	allowed := false
	for _, value := range member.Roles {
		if bot.rolesToIDMap[g.ID]["awokenlead"] == value ||
			bot.rolesToIDMap[g.ID]["awokendev"] == value ||
			bot.rolesToIDMap[g.ID]["staff"] == value ||
			bot.rolesToIDMap[g.ID]["communitymanager"] == value {
			allowed = true
			break
		}
	}

	if !allowed {
		bot.send(member.User.ID, "You are not allowed to run this function", c, g, s)
		return
	}

	if len(args) != 1 {
		bot.send(member.User.ID, "Please use "+bot.prefix+" removePlayer USER", c, g, s)
		return
	}

	userID, err := bot.getUserID(args[0], s, g)
	if err != nil {
		bot.send(member.User.ID, "Could not detect user. Please try again. "+err.Error(), c, g, s)
		return
	}

	// Remove Role from website
	// id 9 = tester
	_, err = bot.RemoveRoleByID.Exec("9", userID)
	if err != nil {
		log.Errorln("Failed removing role from user", err.Error())
		bot.send(member.User.ID, "Could remove from website. "+err.Error(), c, g, s)
	}

	var discordID string
	err = bot.GetDiscordIDByID.QueryRow(userID).Scan(&discordID)
	if err != nil {
		log.Errorln("Could not find discordID")
		bot.send(member.User.ID, "Could not find discordID. "+err.Error(), c, g, s)
		return
	}

	// Remove discord role
	s.GuildMemberRoleRemove(g.ID, discordID, bot.rolesToIDMap[g.ID]["tester"])

	bot.send(member.User.ID, "Removed Player role from user.", c, g, s)
}
