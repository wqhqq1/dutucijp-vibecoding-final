package main
import("database/sql";"encoding/json";"os";"path/filepath";"time";_ "modernc.org/sqlite")
func main(){
 db,e:=sql.Open("sqlite","file:"+filepath.ToSlash(os.Args[1])+"?mode=ro&_pragma=cache_size(-65536)&_pragma=mmap_size(536870912)&_pragma=temp_store(2)");if e!=nil{panic(e)};defer db.Close();db.SetMaxOpenConns(1)
 var run string;if e=db.QueryRow("SELECT id FROM runs WHERE status='published' LIMIT 1").Scan(&run);e!=nil{panic(e)}
 queries:=[]struct{Name,SQL string}{
 {"correlated",`SELECT count(*) FROM sale_balances b WHERE b.run_id=? AND (b.allocated<>coalesce((SELECT sum(a.quantity) FROM allocations a WHERE a.run_id=b.run_id AND a.sale_id=b.sale_id),0) OR b.original-b.allocated<>b.remaining OR b.remaining<0)`},
 {"grouped",`WITH allocated AS MATERIALIZED (SELECT sale_id,sum(quantity) AS quantity FROM allocations WHERE run_id=? GROUP BY sale_id) SELECT count(*) FROM sale_balances b LEFT JOIN allocated a ON a.sale_id=b.sale_id WHERE b.run_id=? AND (b.allocated<>coalesce(a.quantity,0) OR b.original-b.allocated<>b.remaining OR b.remaining<0)`},
 {"grouped_combined",`WITH allocated AS MATERIALIZED (SELECT sale_id,sum(quantity) AS quantity FROM allocations WHERE run_id=? GROUP BY sale_id) SELECT count(*),coalesce(sum(b.allocated<>coalesce(a.quantity,0) OR b.original-b.allocated<>b.remaining OR b.remaining<0),0) FROM sale_balances b LEFT JOIN allocated a ON a.sale_id=b.sale_id WHERE b.run_id=?`},
 }
 enc:=json.NewEncoder(os.Stdout)
 for repeat:=0;repeat<2;repeat++ { for _,q:=range queries { started:=time.Now();var bad,count int;args:=[]any{run};if q.Name!="correlated"{args=append(args,run)};if q.Name=="grouped_combined"{e=db.QueryRow(q.SQL,args...).Scan(&count,&bad)}else{e=db.QueryRow(q.SQL,args...).Scan(&bad)};if e!=nil{panic(e)};enc.Encode(map[string]any{"query":q.Name,"repeat":repeat+1,"seconds":time.Since(started).Seconds(),"bad":bad,"count":count}) } }
}
