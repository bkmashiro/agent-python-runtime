package main

import("context";"encoding/json";"testing")

func TestOfflineEmptyHistoryNeverDispatches(t *testing.T){
 calls:=0
 fixture:=&taskEpisode{Domains:[]domainDeclaration{{Name:"x",Call:func(context.Context,json.RawMessage)(any,error){calls++;return 7,nil}}}}
 rt:=&episodeRuntime{fixture:fixture,playback:true}
 if _,err:=rt.callDomain("x",context.Background(),json.RawMessage(`{}`));err==nil||rt.replayErr==nil{t.Fatal("missing direct history accepted")}
 journal:=&playbackToolJournal{runtime:rt}
 if _,err:=journal.Call(context.Background(),"x",json.RawMessage(`{}`),func(context.Context)[]byte{calls++;return []byte(`{"value":7}`)});err==nil{t.Fatal("missing Python history accepted")}
 if calls!=0{t.Fatal("offline path called live dispatcher")}
}

func TestRawProviderBindingRejectsDivergence(t *testing.T){
 messages:=[]Message{{Role:"user",Content:"hi"}}
 request,_:=encodeProviderRequestBody("fixture",messages,nil,64)
 message:=Message{Role:"assistant",Content:"done",ReasoningContent:"private trace field"}
 body,_:=json.Marshal(map[string]any{"model":"fixture","choices":[]any{map[string]any{"message":message}}})
 call:=privateProviderExchange{Completed:true,Transport:"http",StatusCode:200,Messages:messages,MaxTokens:64,RequestBody:request,ResponseBody:body,Response:ProviderResponse{Model:"fixture",Message:message}}
 if err:=validateProviderExchange(call,"fixture");err!=nil{t.Fatal(err)}
 call.Response.Message.Content="changed"
 if err:=validateProviderExchange(call,"fixture");err==nil{t.Fatal("parsed response can override raw response")}
}
