package main

import (
	"strconv"

	"github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
)

func (bot *AwakenBot) cmdSync(s *discordgo.Session, c *discordgo.Channel, g *discordgo.Guild, m *discordgo.MessageCreate, args []string) {

	// Find the member from the message
	member, err := s.State.Member(g.ID, m.Author.ID)
	if err != nil {
		// Could not find member.
		log.Errorln("Could not find member", err)
		return
	}

	allowed := false
	for _, value := range member.Roles {
		if bot.rolesToIDMap[g.ID]["awokenlead"] == value {
			allowed = true
			break
		}
	}

	if !allowed {
		bot.send(member.User.ID, "You are not allowed to run this function", c, g, s)
		return
	}

	if len(args) != 1 {
		bot.send(member.User.ID, "Please use "+bot.prefix+" syncRole ROLENAME", c, g, s)
		return
	}

	if _, ok := bot.rolesToIDMap[g.ID][args[0]]; !ok {
		bot.send(member.User.ID, "Unknown role", c, g, s)
		return
	}

	var id, title, slug string
	err = bot.GetRoleBySlug.QueryRow(args[0]).Scan(&id, &title, &slug)
	if err != nil {
		log.Noteln("Could not get role!", err)
		return
	}

	members := bot.getMembersByRole(bot.rolesToIDMap[g.ID][args[0]], g)
	var queryArgs []interface{}
	queryArgs = append(queryArgs, id)
	for _, member := range members {
		queryArgs = append(queryArgs, member.User.ID)
	}

	_, err = bot.updateUsersByDiscordId(len(members)).Exec(queryArgs...)
	if err != nil {
		log.Errorln("Failed setting all roles for members", err.Error())
	}

	bot.send(member.User.ID, "Assigned "+args[0]+" to "+strconv.Itoa(len(members))+" members", c, g, s)
}
