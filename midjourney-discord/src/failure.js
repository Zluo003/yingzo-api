// Only recognized Discord diagnostics become public reasons; never return raw messages or prompts.
export function discordFailure(error, timedOut = false) {
  if (timedOut) return "discord_task_timeout";
  if (error?.midjourneyCode === "manual_action_required") return "manual_action_required";
  const title = typeof error?.midjourneyTitle === "string" ? error.midjourneyTitle : "";
  const description = typeof error?.midjourneyDescription === "string" ? error.midjourneyDescription : "";
  if (/^(?:banned prompt detected|prompt blocked|content policy violation)$/i.test(title.trim()) ||
      /\b(?:AI moderator thinks this prompt|against our community guidelines)\b/i.test(description)) {
    return "content_policy_violation";
  }
  if (/^Invalid\b/i.test(title.trim())) return "invalid_parameters";
  return "discord_task_failed";
}
