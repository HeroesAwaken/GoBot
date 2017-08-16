package main

import (
	"database/sql"
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

	userID, err := bot.getUserID(args[0], s, g)
	if err != nil {
		bot.send(member.User.ID, "Could not detect user. Please try again. "+err.Error(), c, g, s)
		return
	}

	log.Debugln(userID)

	bot.discordStats(userID, c, s, g, m, member)
}

func (bot *AwakenBot) discordStats(identifier string, c *discordgo.Channel, s *discordgo.Session, g *discordgo.Guild, m *discordgo.MessageCreate, member *discordgo.Member) {
	var err error
	var id, username, email, birthday, ipAddress, discordName, discordEmail, discordDiscriminator, discordID sql.NullString
	err = bot.GetUserWithDiscord.QueryRow(identifier).Scan(&id, &username, &email, &birthday, &ipAddress, &discordName, &discordEmail, &discordDiscriminator, &discordID)
	if err != nil {
		bot.send(member.User.ID, "Could get user info. Please try again. "+err.Error(), c, g, s)
		return
	}

	rows, err := bot.GetUserRolesByID.Query(identifier)
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
		SetTitle("User info on "+username.String).
		AddField("ID", id.String).
		AddField("Username", username.String).
		AddField("eMail", email.String).
		AddField("Birthday", birthday.String).
		AddField("IP-Address", ipAddress.String).
		AddField("Discord", discordName.String+"#"+discordDiscriminator.String).
		AddField("Discord-ID", discordID.String).
		AddField("Discord-eMail", discordEmail.String).
		AddField("Roles", strings.Join(roles, ", ")).
		SetColor(0x00ff00).
		InlineAllFields().
		MessageEmbed
	_, err = s.ChannelMessageSendEmbed(m.ChannelID, embed)
	if err != nil {
		log.Errorln(err)
	}
}

func (bot *AwakenBot) getUserID(userString string, s *discordgo.Session, g *discordgo.Guild) (string, error) {
	var err error

	// Check if it's a discord user mention
	found := bot.regexUserID.FindStringSubmatch(userString)
	if len(found) == 2 {
		mentionUserID := found[1]
		member, err := s.State.Member(g.ID, mentionUserID)
		if err != nil {
			// Could not find member.
			log.Errorln("Could not find member", err)
			return "", errors.New("Could not find member")
		}
		var id string
		err = bot.GetIDByDiscordID.QueryRow(member.User.ID).Scan(&id)
		if err != nil {
			return "", errors.New("Could net get user info by hero")
		}
		return id, nil
	}

	args := strings.Split(userString, ":")
	if len(args) != 2 {
		return "", errors.New("Invalid format. please use TYPE:NICK. E.g. website:Makahost or hero:RoyalMaka")
	}

	switch args[0] {
	case "discord":
		var id string
		err = bot.GetIDByDiscordID.QueryRow(args[1]).Scan(&id)
		if err != nil {
			return "", errors.New("Could net get user info by discord")
		}
		return id, nil
	case "hero":
		var id string
		err = bot.GetIDByHero.QueryRow(args[1]).Scan(&id)
		if err != nil {
			return "", errors.New("Could net get user info by hero")
		}
		return id, nil
	case "website":
		var id string
		err = bot.GetIDByUser.QueryRow(args[1]).Scan(&id)
		if err != nil {
			return "", errors.New("Could net get user info by user")
		}
		return id, nil
	}

	return args[1], nil
}
