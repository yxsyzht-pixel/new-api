package chatrecord

import "testing"

// These are real openers taken from recorded traffic, which is the only honest
// way to judge the rules: they were written by looking at what clients actually
// send, not at what one imagines they send.
func TestClassifySourceOnRealOpeners(t *testing.T) {
	machine := []string{
		"The following is the Codex agent history added since your last approval",
		"The following command was flagged as: script execution via -e/-c",
		"Output format requirement: valid json.",
		"[The user sent an image~ Here's what I can see:\nThe image is a screenshot",
		"[Workspace::v1: /home/wip/webui-workspace]\n[@CS商品补单员](bot:5) 请处理",
		"[Your active task list was preserved across context compression",
		"【网站 AI 副驾工作模式】\n你是运行在用户 Windows 电脑上的本地 Hermes 智能体",
		"You are a summarization agent creating a context checkpoint.",
		"You are a helpful assistant. You will be presented with a user request",
		"<codex_delegation>\n  <source_thread_id>01a0279a</source_thread_id>",
		"<heartbeat>\r\n  <automation_id>cocoon-t-1</automation_id>",
		"<subagent_notification>\n{\"agent_path\":\"01a0282d\"}",
		"",
	}
	for _, message := range machine {
		if got := ClassifySource(message, nil); got != SourceAuto {
			t.Errorf("ClassifySource(%.48q) = %s, want %s", message, got, SourceAuto)
		}
	}

	people := []string{
		"完成了么？",
		"好的，你解决阻塞，然后马上发布，我看看效果",
		"这一版比之前更接近“奢品同行”的方向，但我希望再明确一个核心：",
		"当前工作台的美观度不够，我们做个调整，在背景中加入非常轻微的颗粒纹理",
		"ARIOSEYEARS 26夏市场调研 https://xhslink.cn/o/qErsixmUI4",
		"Implement Task 6 only in D:\\Codex\\.worktrees\\self-media-studio",
		"Perform Task 6 code-quality review after spec compliance fix.",
		// Shapes that look structural but are not: a person may well write these.
		"<- this arrow is not an element",
		"<3 nice work",
	}
	for _, message := range people {
		if got := ClassifySource(message, nil); got != SourceHuman {
			t.Errorf("ClassifySource(%.48q) = %s, want %s", message, got, SourceHuman)
		}
	}
}

// House prompt templates read like ordinary instructions — no general rule can
// tell them apart, which is why the operator can name them.
func TestOperatorPatternsCatchHouseTemplates(t *testing.T) {
	templates := []string{
		"Review the conversation above and update the skill library. Be concise.",
		"Fully describe and explain everything about this image, then answer",
	}
	patterns := []string{
		"update the skill library",
		"Fully describe and explain everything about this image",
	}

	for _, message := range templates {
		if got := ClassifySource(message, nil); got != SourceHuman {
			t.Fatalf("without the operator's list %.40q should read as a person's words", message)
		}
		if got := ClassifySource(message, patterns); got != SourceAuto {
			t.Errorf("with the operator's list, %.40q = %s, want %s", message, got, SourceAuto)
		}
	}
}
