import json
catalog=sources.demo_catalog()
manifest=sources.benchmark_manifest()
ranked=sorted([{'id':item['id'],'score':item['score'],'title':item['title']} for item in catalog],key=lambda item:(-item['score'],item['id']))
suite={'id':manifest['suite']['id'],'version':manifest['suite']['version'],'case_ids':sorted([item['id'] for item in manifest['cases']])}
report={'catalog':ranked,'suite':suite}
with open('/workspace/structured-report.json','w',encoding='utf-8') as handle:
    handle.write(json.dumps(report,sort_keys=True,separators=(',',':')))
result={'top_id':ranked[0]['id'],'suite_id':suite['id'],'case_count':len(suite['case_ids'])}