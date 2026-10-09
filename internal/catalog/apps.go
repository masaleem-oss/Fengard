package catalog

import "sort"

type App struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Group   string   `json:"group"`
	Domains []string `json:"-"`
}

var Apps = []App{
	{ID: "tiktok", Name: "TikTok", Group: "social", Domains: []string{"tiktok.com", "tiktokv.com", "tiktokcdn.com", "tiktokcdn-us.com", "byteoversea.com", "musical.ly", "ibytedtos.com", "ibyteimg.com"}},
	// instagram media comes off metas shared cdn so blocking it hits facebook too
	{ID: "instagram", Name: "Instagram", Group: "social", Domains: []string{"instagram.com", "cdninstagram.com", "ig.me", "instagr.am", "igcdn.com", "igsonar.com", "fbcdn.net", "fbsbx.com"}},
	{ID: "snapchat", Name: "Snapchat", Group: "social", Domains: []string{"snapchat.com", "snapkit.com", "sc-cdn.net", "snap.com", "sc-gw.com", "snapads.com"}},
	{ID: "facebook", Name: "Facebook & Messenger", Group: "social", Domains: []string{"facebook.com", "fbcdn.net", "messenger.com", "fb.com", "facebook.net"}},
	{ID: "x", Name: "X (Twitter)", Group: "social", Domains: []string{"x.com", "twitter.com", "twimg.com", "t.co"}},
	{ID: "reddit", Name: "Reddit", Group: "social", Domains: []string{"reddit.com", "redd.it", "redditmedia.com", "redditstatic.com"}},
	{ID: "threads", Name: "Threads", Group: "social", Domains: []string{"threads.net", "threads.com", "cdninstagram.com", "fbcdn.net"}},
	{ID: "pinterest", Name: "Pinterest", Group: "social", Domains: []string{"pinterest.com", "pinimg.com"}},
	{ID: "tumblr", Name: "Tumblr", Group: "social", Domains: []string{"tumblr.com"}},
	{ID: "bereal", Name: "BeReal", Group: "social", Domains: []string{"bereal.com", "bere.al"}},
	{ID: "omegle", Name: "Omegle-style chat sites", Group: "social", Domains: []string{"omegle.com", "ome.tv", "chatroulette.com", "emeraldchat.com", "monkey.app"}},

	{ID: "youtube", Name: "YouTube", Group: "video", Domains: []string{"youtube.com", "youtu.be", "ytimg.com", "googlevideo.com", "youtube-nocookie.com", "youtubei.googleapis.com", "youtubekids.com"}},
	{ID: "netflix", Name: "Netflix", Group: "video", Domains: []string{"netflix.com", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net"}},
	{ID: "twitch", Name: "Twitch", Group: "video", Domains: []string{"twitch.tv", "twitchcdn.net", "jtvnw.net", "ttvnw.net"}},
	{ID: "disneyplus", Name: "Disney+", Group: "video", Domains: []string{"disneyplus.com", "disney-plus.net", "bamgrid.com", "dssott.com"}},
	{ID: "primevideo", Name: "Prime Video", Group: "video", Domains: []string{"primevideo.com", "aiv-cdn.net", "aiv-delivery.net"}},
	{ID: "kick", Name: "Kick", Group: "video", Domains: []string{"kick.com"}},
	{ID: "hulu", Name: "Hulu", Group: "video", Domains: []string{"hulu.com", "hulustream.com"}},
	{ID: "crunchyroll", Name: "Crunchyroll", Group: "video", Domains: []string{"crunchyroll.com", "vrv.co"}},

	{ID: "roblox", Name: "Roblox", Group: "games", Domains: []string{"roblox.com", "rbxcdn.com", "roblox.cn", "rbx.com"}},
	{ID: "fortnite", Name: "Fortnite & Epic Games", Group: "games", Domains: []string{"fortnite.com", "epicgames.com", "epicgames.dev", "unrealengine.com"}},
	{ID: "minecraft", Name: "Minecraft", Group: "games", Domains: []string{"minecraft.net", "minecraftservices.com", "mojang.com", "xboxlive.com"}},
	{ID: "steam", Name: "Steam", Group: "games", Domains: []string{"steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamserver.net", "valvesoftware.com"}},
	{ID: "playstation", Name: "PlayStation Network", Group: "games", Domains: []string{"playstation.com", "playstation.net", "sonyentertainmentnetwork.com"}},
	{ID: "xbox", Name: "Xbox Live", Group: "games", Domains: []string{"xbox.com", "xboxlive.com", "xboxservices.com"}},
	{ID: "nintendo", Name: "Nintendo Online", Group: "games", Domains: []string{"nintendo.net", "nintendo.com"}},
	{ID: "riot", Name: "League of Legends & Valorant", Group: "games", Domains: []string{"riotgames.com", "leagueoflegends.com", "playvalorant.com", "riotcdn.net"}},
	{ID: "blizzard", Name: "Battle.net", Group: "games", Domains: []string{"blizzard.com", "battle.net"}},
	{ID: "supercell", Name: "Clash & Brawl Stars", Group: "games", Domains: []string{"supercell.com", "clashofclans.com", "brawlstars.com", "supercellgames.com"}},
	{ID: "pubg", Name: "PUBG", Group: "games", Domains: []string{"pubg.com", "pubgmobile.com", "krafton.com"}},
	{ID: "callofduty", Name: "Call of Duty", Group: "games", Domains: []string{"callofduty.com", "activision.com"}},
	{ID: "genshin", Name: "Genshin & HoYoverse", Group: "games", Domains: []string{"hoyoverse.com", "mihoyo.com", "hoyolab.com"}},
	{ID: "browsergames", Name: "Browser game sites", Group: "games", Domains: []string{"poki.com", "crazygames.com", "coolmathgames.com", "friv.com", "miniclip.com", "agar.io", "krunker.io", "y8.com", "kizi.com"}},

	{ID: "discord", Name: "Discord", Group: "chat", Domains: []string{"discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media"}},
	{ID: "whatsapp", Name: "WhatsApp", Group: "chat", Domains: []string{"whatsapp.com", "whatsapp.net", "wa.me"}},
	{ID: "telegram", Name: "Telegram", Group: "chat", Domains: []string{"telegram.org", "t.me", "telegram.me", "telesco.pe"}},
	{ID: "signal", Name: "Signal", Group: "chat", Domains: []string{"signal.org", "whispersystems.org"}},
	{ID: "wechat", Name: "WeChat", Group: "chat", Domains: []string{"wechat.com", "weixin.qq.com", "wx.qq.com"}},

	{ID: "chatgpt", Name: "ChatGPT", Group: "ai", Domains: []string{"chatgpt.com", "chat.openai.com", "openai.com", "oaiusercontent.com", "oaistatic.com"}},
	{ID: "claude", Name: "Claude", Group: "ai", Domains: []string{"claude.ai", "anthropic.com"}},
	{ID: "gemini", Name: "Gemini", Group: "ai", Domains: []string{"gemini.google.com", "bard.google.com", "aistudio.google.com"}},
	{ID: "copilot", Name: "Microsoft Copilot", Group: "ai", Domains: []string{"copilot.microsoft.com"}},
	{ID: "characterai", Name: "Character.AI", Group: "ai", Domains: []string{"character.ai", "c.ai"}},
	{ID: "perplexity", Name: "Perplexity", Group: "ai", Domains: []string{"perplexity.ai"}},
	{ID: "deepseek", Name: "DeepSeek", Group: "ai", Domains: []string{"deepseek.com"}},
	{ID: "grok", Name: "Grok", Group: "ai", Domains: []string{"grok.com", "x.ai"}},

	{ID: "amazon", Name: "Amazon shopping", Group: "shopping", Domains: []string{"amazon.com", "amazon.com.au", "amazon.co.uk", "amazon.de", "amazon.ca", "amazon.in", "amazon.co.jp"}},
	{ID: "temu", Name: "Temu", Group: "shopping", Domains: []string{"temu.com"}},
	{ID: "shein", Name: "Shein", Group: "shopping", Domains: []string{"shein.com", "sheinsz.ltd"}},
	{ID: "aliexpress", Name: "AliExpress", Group: "shopping", Domains: []string{"aliexpress.com", "aliexpress.us", "alicdn.com"}},
	{ID: "ebay", Name: "eBay", Group: "shopping", Domains: []string{"ebay.com", "ebay.com.au", "ebay.co.uk", "ebayimg.com"}},

	{ID: "spotify", Name: "Spotify", Group: "other", Domains: []string{"spotify.com", "scdn.co", "spotifycdn.com"}},
	{ID: "onlyfans", Name: "OnlyFans", Group: "other", Domains: []string{"onlyfans.com", "onlyfans.app"}},
	{ID: "dating", Name: "Dating apps", Group: "other", Domains: []string{"tinder.com", "gotinder.com", "bumble.com", "hinge.co", "grindr.com", "okcupid.com", "match.com", "badoo.com"}},
	{ID: "torrents", Name: "Torrent sites", Group: "other", Domains: []string{"thepiratebay.org", "1337x.to", "rarbg.to", "yts.mx", "nyaa.si", "torrentgalaxy.to", "limetorrents.lol"}},
}

var appIndex = func() map[string]int {
	m := make(map[string]int, len(Apps))
	for i, a := range Apps {
		m[a.ID] = i
	}
	return m
}()

func AppIndex(id string) int {
	if i, ok := appIndex[id]; ok {
		return i
	}
	return -1
}

var AppGroups = []struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}{
	{"social", "Social media"}, {"video", "Video & streaming"}, {"games", "Games"},
	{"chat", "Messaging"}, {"ai", "AI chatbots"}, {"shopping", "Shopping"}, {"other", "Other"},
}

// value is index plus 1 so zero means none
type AppSet struct{ set *DomainSet }

func BuildAppSet() *AppSet {
	b := NewBuilder()
	for i, a := range Apps {
		for _, d := range a.Domains {
			b.Add(d, Mask(i+1))
		}
	}
	return &AppSet{set: b.Build()}
}

func (a *AppSet) Lookup(domain string) int {
	if a == nil {
		return -1
	}
	for d := domain; d != ""; d = Parent(d) {
		if v := a.set.Get(d); v != 0 {
			return int(v) - 1
		}
	}
	return -1
}

func AppIDsSorted() []string {
	ids := make([]string, len(Apps))
	for i, a := range Apps {
		ids[i] = a.ID
	}
	sort.Strings(ids)
	return ids
}
