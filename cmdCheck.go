package main

import (
	"errors"
	"strings"

	"github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/bwmarrin/discordgo"
	_ "github.com/go-sql-driver/mysql"
)

func (bot *AwakenBot) cmdCheck(s *discordgo.Session, c *discordgo.Channel, g *discordgo.Guild, m *discordgo.MessageCreate, args []string) {

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
		bot.send(member.User.ID, "Please use "+bot.prefix+" check USER", c, g, s)
		return
	}

	identifierType, identifier, err := bot.getUser(args[0], s, g)
	if err != nil {
		bot.send(member.User.ID, "Could not detect user. Please try again. "+err.Error(), c, g, s)
		return
	}

	switch identifierType {
	case "discord":
		bot.discordStats(identifier, c, s, g, m, member)
	case "website":
		bot.websiteStats(identifier, c, s, g, m, member)
	case "hero":
		bot.heroStats(identifier, c, s, g, m, member)
	}
}

func (bot *AwakenBot) websiteStats(identifier string, c *discordgo.Channel, s *discordgo.Session, g *discordgo.Guild, m *discordgo.MessageCreate, member *discordgo.Member) {
	var err error
	var discordID string
	err = bot.GetDiscordIDByUser.QueryRow(identifier).Scan(&discordID)
	if err != nil {
		bot.send(member.User.ID, "Could get user info. Please try again. "+err.Error(), c, g, s)
		return
	}

	bot.discordStats(discordID, c, s, g, m, member)
}

func (bot *AwakenBot) heroStats(identifier string, c *discordgo.Channel, s *discordgo.Session, g *discordgo.Guild, m *discordgo.MessageCreate, member *discordgo.Member) {
	var err error
	var discordID string
	err = bot.GetDiscordIDByHero.QueryRow(identifier).Scan(&discordID)
	if err != nil {
		bot.send(member.User.ID, "Could get user info. Please try again. "+err.Error(), c, g, s)
		return
	}

	bot.discordStats(discordID, c, s, g, m, member)
}

func (bot *AwakenBot) discordStats(identifier string, c *discordgo.Channel, s *discordgo.Session, g *discordgo.Guild, m *discordgo.MessageCreate, member *discordgo.Member) {
	var err error
	var id, username, email, birthday, ipAddress, discordName, discordEmail, discordDiscriminator string
	err = bot.GetUserByDiscordID.QueryRow(identifier).Scan(&id, &username, &email, &birthday, &ipAddress, &discordName, &discordEmail, &discordDiscriminator)
	if err != nil {
		bot.send(member.User.ID, "Could get user info. Please try again. "+err.Error(), c, g, s)
		return
	}

	rows, err := bot.GetUserRolesByDiscordID.Query(identifier)
	defer rows.Close()
	if err != nil {
		bot.send(member.User.ID, "Could get user roles. Please try again. "+err.Error(), c, g, s)
		return
	}

	roles := []string{}
	for rows.Next() {
		var slug string

		err := rows.Scan(&slug)
		if err != nil {
			log.Errorln("Issue with database:", err.Error())
		}

		roles = append(roles, slug)
	}

	embed := NewEmbed().
		SetTitle("User info on "+username).
		AddField("ID", id).
		AddField("Username", username).
		AddField("eMail", email).
		AddField("Birthday", birthday).
		AddField("IP-Address", ipAddress).
		AddField("Discord", discordName+"#"+discordDiscriminator).
		AddField("Discord-ID", identifier).
		AddField("Discord-eMail", discordEmail).
		AddField("Roles", strings.Join(roles, ", ")).
		SetColor(0x00ff00).
		InlineAllFields().
		MessageEmbed
	_, err = s.ChannelMessageSendEmbed(m.ChannelID, embed)
	if err != nil {
		log.Errorln(err)
	}
}

func (bot *AwakenBot) getUser(userString string, s *discordgo.Session, g *discordgo.Guild) (string, string, error) {

	// Check if it's a discord user mention
	found := bot.regexUserID.FindStringSubmatch(userString)
	if len(found) == 2 {
		mentionUserID := found[1]
		member, err := s.State.Member(g.ID, mentionUserID)
		if err != nil {
			// Could not find member.
			log.Errorln("Could not find member", err)
			return "", "", errors.New("Could not find member")
		}
		return "discord", member.User.ID, nil
	}

	args := strings.Split(userString, ":")
	if len(args) != 2 {
		return "", "", errors.New("Invalid format. please use TYPE:NICK. E.g. website:Makahost or hero:RoyalMaka")
	}

	log.Debugln(args[0], args[1])

	return args[0], args[1], nil
}
