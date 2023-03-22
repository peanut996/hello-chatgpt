package handler

import (
	"chatgpt-bot/bot/telegram"
	"chatgpt-bot/constant/cmd"
	botError "chatgpt-bot/constant/error"
	"chatgpt-bot/constant/tip"
	"chatgpt-bot/model/persist"
	"chatgpt-bot/repository"
	"chatgpt-bot/utils"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"
)

type BotCmd = string

type CommandHandler interface {
	Cmd() BotCmd
	Run(t telegram.TelegramBot, message tgbotapi.Message) error
}

type StatusCommandHandler struct {
	userRepository             *repository.UserRepository
	userInviteRecordRepository *repository.UserInviteRecordRepository
}

func (s *StatusCommandHandler) Cmd() BotCmd {
	return cmd.STATUS
}

func (s *StatusCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	if !b.IsBotAdmin(message.From.ID) {
		return nil
	}
	userCount, err := s.userRepository.Count()
	if err != nil {
		return err
	}

	inviteRecordCount, err := s.userInviteRecordRepository.Count()
	if err != nil {
		return err
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf(tip.StatusTipTemplate, userCount, inviteRecordCount))
	b.SafeSend(msg)
	return nil
}

func NewStatusCommandHandler(userRepository *repository.UserRepository, userInviteRecordRepository *repository.UserInviteRecordRepository) *StatusCommandHandler {
	return &StatusCommandHandler{
		userRepository:             userRepository,
		userInviteRecordRepository: userInviteRecordRepository,
	}
}

type PushCommandHandler struct {
	userRepository *repository.UserRepository
}

func NewPushCommandHandler(userRepository *repository.UserRepository) *PushCommandHandler {
	return &PushCommandHandler{
		userRepository: userRepository,
	}
}

func (p *PushCommandHandler) Cmd() BotCmd {
	return cmd.PUSH
}

func (p *PushCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	if !b.IsBotAdmin(message.From.ID) {
		return fmt.Errorf(tip.NotAdminTip)
	}

	text := tip.DonateTip

	if utils.IsNotEmpty(message.CommandArguments()) {
		text = message.CommandArguments()
	}

	userIDs, err := p.userRepository.GetAllUserID()
	if err != nil {
		return err
	}

	for _, userID := range userIDs {
		go func(userID string, text string) {
			if utils.IsEmpty(userID) {
				return
			}
			uid, _ := utils.StringToInt64(userID)
			msg := tgbotapi.NewMessage(uid, text)
			msg.ParseMode = tgbotapi.ModeMarkdown
			b.SafeSend(msg)
		}(userID, text)
	}

	return nil
}

type DonateCommandHandler struct{}

func (d *DonateCommandHandler) Cmd() BotCmd {
	return cmd.DONATE
}

func (d *DonateCommandHandler) Run(bot telegram.TelegramBot, message tgbotapi.Message) error {

	msg := tgbotapi.NewMessage(message.Chat.ID, tip.DonateTip)
	msg.ParseMode = tgbotapi.ModeMarkdown
	bot.SafeSend(msg)

	return nil
}

type QueryCommandHandler struct {
	userRepository             *repository.UserRepository
	userInviteRecordRepository *repository.UserInviteRecordRepository
}

func (q *QueryCommandHandler) Cmd() BotCmd {
	return cmd.QUERY
}

func (q *QueryCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	userID := utils.Int64ToString(message.From.ID)
	user, err := q.userRepository.GetByUserID(userID)
	if err != nil {
		log.Printf("[QueryCommandHandler] get user by user id failed, err: 【%s】\n", err)
		return err
	}
	if user == nil {
		userInfo, err := b.GetUserInfo(message.From.ID)
		if err != nil {
			return err
		}
		err = q.userRepository.InitUser(userID, userInfo.String())
		if err != nil {
			log.Printf("[QueryCommandHandler] init user failed, err: 【%s】\n", err)
			return err
		}
		user, err = q.userRepository.GetByUserID(userID)
		if err != nil {
			log.Printf("[QueryCommandHandler] get user by user id failed, err: 【%s】\n", err)
			return err
		}
	}
	inviteCount, err := q.userInviteRecordRepository.CountByUserID(userID)
	if err != nil {
		log.Printf("[QueryCommandHandler] get user invite count by user id failed, err: 【%s】\n", err)
		return err
	}

	text := fmt.Sprintf(tip.QueryUserInfoTemplate,
		userID, user.RemainCount, inviteCount, b.GetBotInviteLink(user.InviteCode))
	b.SafeReplyMsg(message.Chat.ID, message.MessageID, text)
	return nil
}

func NewQueryCommandHandler(userRepository *repository.UserRepository, userInviteRecordRepository *repository.UserInviteRecordRepository) *QueryCommandHandler {
	return &QueryCommandHandler{
		userRepository,
		userInviteRecordRepository,
	}
}

type StartCommandHandler struct {
	userRepository             *repository.UserRepository
	userInviteRecordRepository *repository.UserInviteRecordRepository
}

func (c *StartCommandHandler) Cmd() BotCmd {
	return cmd.START
}

func matchInviteCode(code string) bool {
	return utils.IsNotEmpty(code) && len(code) == 10 && utils.IsMatchString(`^[a-zA-Z]{10}$`, code)
}

func (c *StartCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	log.Println(fmt.Printf("get args: [%s]", message.CommandArguments()))
	args := message.CommandArguments()
	if matchInviteCode(args) {
		err := c.handleInvitation(args, utils.Int64ToString(message.From.ID), b)
		if err != nil {
			log.Printf("[StartCommandHandler] handle invitation failed, err: 【%s】", err)
		}
	}
	b.SafeSendMsg(message.Chat.ID, tip.BotStartTip)
	return nil
}

func (c *StartCommandHandler) handleInvitation(inviteCode string, inviteUserID string, b telegram.TelegramBot) error {
	user, err := c.userRepository.GetUserByInviteCode(inviteCode)
	if err != nil {
		log.Printf("[handleInvitation] find user by invite code failed, err: 【%s】", err)
		return err
	}
	if user == nil {
		log.Printf("[handleInvitation] find user by invite code failed, user is nil")
		return errors.New("no such user by invite code: " + inviteCode)
	}
	if user.UserID == inviteUserID {
		log.Printf("[handleInvitation] user can not invite himself")
		return fmt.Errorf("[handleInvitation] user can not invite himself, user id: [%s]", inviteUserID)
	}
	record, err := c.userInviteRecordRepository.GetByInviteUserID(inviteUserID)
	if err != nil {
		log.Printf("[handleInvitation] find user by invite user id failed, err: 【%s】", err)
		return err
	}
	if record != nil {
		log.Printf("[handleInvitation]  user has been invited by other user: " + record.UserID)
		return nil
	}
	inviteRecord := persist.NewUserInviteRecord(user.UserID, inviteUserID)
	err = c.userInviteRecordRepository.Insert(inviteRecord)
	if err != nil {
		return err
	}
	err = c.userRepository.AddCountWhenInviteOther(user.UserID)
	if err != nil {
		return err
	}
	originUserID, _ := utils.StringToInt64(user.UserID)
	b.SafeSendMsg(originUserID, tip.InviteSuccessTip)
	return nil
}

type PingCommandHandler struct {
}

func (c *PingCommandHandler) Cmd() BotCmd {
	return cmd.PING
}

func (c *PingCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	b.SafeSendMsg(message.Chat.ID, tip.BotPingTip)
	return nil
}

type LimiterCommandHandler struct {
}

func (c *LimiterCommandHandler) Cmd() BotCmd {
	return cmd.LIMITER
}

func (c *LimiterCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	if !b.IsBotAdmin(message.From.ID) {
		msg.Text = tip.NotAdminTip
	} else {
		limiter := utils.ParseBoolString(message.CommandArguments())
		b.Config().RateLimiterConfig.Enable = limiter
		msg.Text = fmt.Sprintf("limiter status is %v now", limiter)
	}
	b.SafeSend(msg)
	return nil
}

type PprofCommandHandler struct {
}

func (c *PprofCommandHandler) Cmd() BotCmd {
	return cmd.PPROF
}

func (c *PprofCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	if !b.IsBotAdmin(message.From.ID) {
		msg.Text = tip.NotAdminTip
		b.SafeSend(msg)
		return nil
	}

	if filePath, success := dumpProfile(); success {
		defer func() {
			_ = os.Remove(filePath)
		}()
		err := sendFile(b, message.Chat.ID, filePath)
		if err == nil {
			return nil
		}
	}

	msg.Text = botError.InternalError
	b.SafeSend(msg)
	return nil
}

func dumpProfile() (string, bool) {
	fileName := fmt.Sprintf("%d.pprof", time.Now().Unix())
	filePath := os.TempDir() + string(os.PathSeparator) + fileName
	tmpFile, err := os.Create(filePath)
	defer func(tmpFile *os.File) {
		_ = tmpFile.Close()
	}(tmpFile)

	if err != nil {
		log.Printf("[DumpProfile] create temp file failed, err: 【%s】", err)
		return err.Error(), false
	}

	err = pprof.WriteHeapProfile(tmpFile)
	if err != nil {
		log.Printf("[DumpProfile] create temp file failed, err: 【%s】", err)
		return err.Error(), false
	}

	return tmpFile.Name(), true
}

func sendFile(b telegram.TelegramBot, chatID int64, filePath string) error {
	fileMsg := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
	_, err := b.GetAPIBot().Send(fileMsg)
	if err != nil {
		log.Printf("[SendFile] send file failed, err: 【%s】", err)
		return err
	}

	return nil
}

type InviteCommandHandler struct {
	userRepository *repository.UserRepository
}

func (i *InviteCommandHandler) Cmd() BotCmd {
	return cmd.INVITE
}

func (i *InviteCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	userID := utils.Int64ToString(message.From.ID)
	user, err := i.userRepository.GetByUserID(userID)
	if err != nil {
		log.Printf("[InviteCommandHandler] find user by user id failed, err: 【%s】", err)
		return err
	}
	if user != nil {
		link := b.GetBotInviteLink(user.InviteCode)
		b.SafeSendMsg(message.Chat.ID, fmt.Sprintf(tip.InviteTipTemplate, link, link))
		return nil
	} else {
		userName := ""
		tgUser, err := b.GetUserInfo(message.From.ID)
		if err == nil {
			userName = tgUser.String()
		}
		err = i.userRepository.InitUser(userID, userName)
		if err != nil {
			log.Printf("[InviteCommandHandler] init user failed, err: 【%s】", err)
			return err
		}
		user, _ := i.userRepository.GetByUserID(userID)
		link := b.GetBotInviteLink(user.InviteCode)
		b.SafeSendMsg(message.Chat.ID, fmt.Sprintf(tip.InviteTipTemplate, link, link))
	}
	return nil
}

type CountCommandHandler struct {
	userRepository *repository.UserRepository
}

func (c *CountCommandHandler) Cmd() BotCmd {
	return cmd.COUNT
}

func (c *CountCommandHandler) Run(b telegram.TelegramBot, message tgbotapi.Message) error {
	if !b.IsBotAdmin(message.From.ID) {
		b.SafeSendMsg(message.Chat.ID, tip.NotAdminTip)
		return nil
	}
	args := message.CommandArguments()
	if args == "" {
		return fmt.Errorf("invalid args")
	}
	params := strings.Split(args, ":")
	if len(params) != 2 {
		return fmt.Errorf("invalid args")
	}
	err := c.userRepository.UpdateCountByUserID(params[0], params[1])
	if err != nil {
		log.Printf("failed to set count. params: %s, err: %s", args, err.Error())
		b.SafeSendMsg(message.Chat.ID, fmt.Sprintf("failed to set count. params: %s, err: %s", args, err.Error()))
		return nil
	}
	b.SafeSendMsg(message.Chat.ID, "success")
	return nil
}

func NewStartCommandHandler(userRepository *repository.UserRepository, userInviteRecordRepository *repository.UserInviteRecordRepository) *StartCommandHandler {
	return &StartCommandHandler{
		userRepository:             userRepository,
		userInviteRecordRepository: userInviteRecordRepository,
	}
}

func NewPingCommandHandler() *PingCommandHandler {
	return &PingCommandHandler{}
}

func NewLimiterCommandHandler() *LimiterCommandHandler {
	return &LimiterCommandHandler{}
}

func NewPprofCommandHandler() *PprofCommandHandler {
	return &PprofCommandHandler{}
}

func NewInviteCommandHandler(userRepository *repository.UserRepository) *InviteCommandHandler {
	return &InviteCommandHandler{
		userRepository: userRepository,
	}
}

func NewCountCommandHandler(userRepository *repository.UserRepository) *CountCommandHandler {
	return &CountCommandHandler{
		userRepository: userRepository,
	}
}

func NewDonateCommandHandler() *DonateCommandHandler {
	return &DonateCommandHandler{}
}
