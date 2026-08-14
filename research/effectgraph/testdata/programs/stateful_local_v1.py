import csv,json
with open('/workspace/metrics.csv','r',encoding='utf-8',newline='') as handle:
    rows=[{'id':row['id'],'value':int(row['value'])} for row in csv.DictReader(handle)]
rows=sorted(rows,key=lambda row:row['id'])
total=sum(row['value'] for row in rows)
normalized=[{'id':row['id'],'share_milli':(row['value']*1000)//total,'value':row['value']} for row in rows]
with open('/workspace/normalized.json','w',encoding='utf-8') as handle:
    handle.write(json.dumps(normalized,sort_keys=True,separators=(',',':')))
summary={'count':len(rows),'max_id':max(rows,key=lambda row:(row['value'],row['id']))['id'],'total':total}
with open('/workspace/summary.json','w',encoding='utf-8') as handle:
    handle.write(json.dumps(summary,sort_keys=True,separators=(',',':')))
result=summary