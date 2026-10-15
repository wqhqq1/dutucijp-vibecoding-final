"""Read-only PreToolUse policy. Never executes the proposed command."""
import json,re,sys

def evaluate(event):
    if event.get('hook_event_name')!='PreToolUse':
        return {}
    data=event.get('tool_input') or {}
    command=str(data.get('command',data.get('cmd','')))
    patterns=[r'(?i)\bRemove-Item\b',r'(?i)\brm\s+[^\n]*(?:-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)',r'(?i)\bgit\s+reset\s+--hard\b',r'(?i)\bgit\s+clean\s+-[a-z]*f',r'(?i)\b(?:del|rmdir)\s+/[sq]']
    if any(re.search(pattern,command) for pattern in patterns):
        return {'hookSpecificOutput':{'hookEventName':'PreToolUse','permissionDecision':'deny','permissionDecisionReason':'Destructive filesystem/history command blocked. Use the documented disposable Hook canary for the demonstration; never target source, original data or private credentials.'}}
    return {}

def main():
    try:
        event=json.load(sys.stdin)
    except (ValueError,TypeError):
        print(json.dumps({'hookSpecificOutput':{'hookEventName':'PreToolUse','permissionDecision':'deny','permissionDecisionReason':'Hook input is not valid JSON; proposed operation was not evaluated.'}}));return
    result=evaluate(event)
    if result:print(json.dumps(result))

if __name__=='__main__':main()
