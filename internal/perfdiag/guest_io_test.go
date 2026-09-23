package perfdiag

import("bytes";"context";"testing")

func TestGuestIOCapturesBytesOnlyWhenEnabled(t *testing.T){
 ctx,sink:=WithGuestIO(context.Background())
 CaptureGuestIO(ctx,string([]byte{255}),string([]byte{254}))
 if !bytes.Equal(sink.Stdout,[]byte{255})||!bytes.Equal(sink.Stderr,[]byte{254}){t.Fatal("raw stream bytes lost")}
 if n:=testing.AllocsPerRun(1000,func(){CaptureGuestIO(context.Background(),"out","err")});n!=0{t.Fatalf("disabled observer allocates: %g",n)}
}
