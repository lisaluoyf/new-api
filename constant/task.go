package constant

type TaskPlatform string

const (
	TaskPlatformSuno         TaskPlatform = "suno"
	TaskPlatformMidjourney                = "mj"
	TaskPlatformApimartVideo              = "apimart-video"
	TaskPlatformOpenAIImage               = "openai-image"
	TaskPlatformMiniMaxH3                 = "minimax-h3"
	TaskPlatformVideoFee                  = "videofee"
)

// This compatibility contract is scoped to the explicitly configured route.
const VideoFeeSeedanceChannelID = 273

const (
	SunoActionMusic  = "MUSIC"
	SunoActionLyrics = "LYRICS"

	TaskActionGenerate          = "generate"
	TaskActionTextGenerate      = "textGenerate"
	TaskActionFirstTailGenerate = "firstTailGenerate"
	TaskActionReferenceGenerate = "referenceGenerate"
	TaskActionRemix             = "remixGenerate"
)

var SunoModel2Action = map[string]string{
	"suno_music":  SunoActionMusic,
	"suno_lyrics": SunoActionLyrics,
}
