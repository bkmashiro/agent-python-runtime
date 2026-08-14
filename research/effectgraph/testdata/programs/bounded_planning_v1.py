import json
candidates=sources.demo_catalog()
trace=[]
for item in sorted(candidates,key=lambda item:item['id']):
    score=item['score']*3+len(item['title'])
    trace.append({'id':item['id'],'score':score})
best=max(trace,key=lambda item:(item['score'],item['id']))
result={'selected_id':best['id'],'selected_score':best['score'],'trace':trace}