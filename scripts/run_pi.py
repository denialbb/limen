import subprocess
import json
import time

p = subprocess.Popen(['pi', '--mode', 'rpc'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

req = {"id":"test", "type":"prompt", "message":"Run `echo hello` as a tool call and finish"}
p.stdin.write(json.dumps(req) + '\n')
p.stdin.flush()

while True:
    line = p.stdout.readline()
    if not line:
        break
    print(line.strip())
    
    try:
        data = json.loads(line)
        if data.get("type") == "agent_end":
            break
    except:
        pass
