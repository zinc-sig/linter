-- Statements PostgreSQL 17 parses without a syntax error. Each case is one
-- statement, or a few, under a "-- case:" line.
-- case: select-basic
select id, name from student where id > 2 order by name desc limit 3;
-- case: select-join-group-having
select c.code, count(*) as n, round(avg(e.grade), 1) as avg_grade from enrollment e join course c on c.id = e.course_id group by c.code having count(*) >= 3;
-- case: natural-join
select * from student natural join enrollment;
-- case: subquery-in
select name from student where id in (select student_id from enrollment where grade > 80);
-- case: subquery-exists
select name from student s where exists (select 1 from enrollment e where e.student_id = s.id and e.grade < 60);
-- case: scalar-subquery
select name, (select max(grade) from enrollment e where e.student_id = s.id) as best from student s;
-- case: derived-table
select t.code, t.n from (select c.code, count(*) n from enrollment e join course c on c.id = e.course_id group by c.code) as t where t.n > 2;
-- case: cte
with top as (select student_id, avg(grade) g from enrollment group by student_id) select s.name, top.g from top join student s on s.id = top.student_id;
-- case: cte-recursive
with recursive n(i) as (select 1 union all select i + 1 from n where i < 5) select * from n;
-- case: window-rank
select name, grade, rank() over (partition by course_id order by grade desc) r from enrollment e join student s on s.id = e.student_id;
-- case: window-frame
select student_id, grade, avg(grade) over (order by grade rows between 1 preceding and 1 following) m from enrollment;
-- case: window-named
select student_id, row_number() over w, lag(grade, 1, 0) over w from enrollment window w as (partition by course_id order by grade);
-- case: union-all
select id from student union all select id from course union select 99;
-- case: intersect-except
select id from student intersect select student_id from enrollment except select 3;
-- case: case-expr
select name, case when id < 3 then 'low' when id < 5 then 'mid' else 'high' end as band from student;
-- case: is-null
select * from student where name is not null and id is null;
-- case: distinct-count
select count(distinct course_id), sum(grade), min(grade), max(grade), std(grade) from enrollment;
-- case: limit-offset
select * from student order by id limit 2 offset 1;
-- case: for-update
select * from student where id = 1 for update;
-- case: lateral
select s.name, t.g from student s, lateral (select max(grade) g from enrollment e where e.student_id = s.id) t;
-- case: insert-values
insert into student (id, name) values (7, 'Gil'), (8, 'Hal');
-- case: insert-select
insert into student (id, name) select id + 100, name from student where id < 3;
-- case: update-simple
update enrollment set grade = grade + 5 where course_id = 2;
-- case: delete-simple
delete from enrollment where grade < 60;
-- case: create-table-as
create table t3 as select * from student;
-- case: create-temporary
create temporary table tmp (a int);
-- case: alter-add-constraint
alter table enrollment add constraint chk_grade check (grade between 0 and 100);
-- case: drop-table
drop table if exists enrollment, nothing_here;
-- case: truncate
truncate table enrollment;
-- case: create-index
create unique index idx_code on course (code);
-- case: drop-view
drop view if exists v;
-- case: start-transaction
start transaction;
-- case: begin
begin;
-- case: commit
commit;
-- case: rollback
rollback;
-- case: savepoint
savepoint sp1;
-- case: rollback-to
rollback to savepoint sp1;
-- case: explain
explain select * from student where id = 1;
-- case: any-all
select * from enrollment where grade > all (select grade from enrollment where course_id = 2) or grade = any (select 1) or grade <> some (select 2);
-- case: row-compare
select * from enrollment where (student_id, course_id) in ((1, 1), (2, 3)) and row(1, 2) = row(1, 2);
-- case: not-in-exists
select * from student s where id not in (1, 2) and not exists (select * from enrollment e where e.student_id = s.id);
-- case: cte-update
with low as (select student_id from enrollment where grade < 60) update enrollment set grade = 60 where student_id in (select student_id from low);
-- case: cte-delete
with low as (select student_id from enrollment where grade < 60) delete from enrollment where student_id in (select student_id from low);
-- case: nested-parens-union
(select id from student order by id limit 1) union (select id from course) order by 1 limit 3;
-- case: join-nested
select * from student s join (enrollment e join course c on c.id = e.course_id) on e.student_id = s.id;
-- case: select-no-from-where
select 1 where 1 = 1;
-- case: frame-range
select sum(grade) over (order by grade range between unbounded preceding and current row), ntile(4) over (order by grade), first_value(grade) over w, nth_value(grade, 2) over w from enrollment window w as (order by grade);
-- case: for-share
select * from student for share skip locked;
-- case: nowait
select * from student for update nowait;
-- case: show-tables
show tables;
-- case: functions
select concat(name, '-', id), ifnull(null, 0), coalesce(null, name), upper(name), length(name), substring(name, 1, 2), if(id > 2, 'a', 'b') from student;
-- case: pg-cast
select grade::int, cast(grade as numeric(5,2)), '2026-01-01'::date + interval '1 day' from enrollment;
-- case: pg-ilike
select * from student where name ilike 'a%' and name ~* '^a' and name similar to 'A%';
-- case: pg-returning
insert into student values (20, 'Zed') returning id, name;
-- case: pg-update-returning
update enrollment set grade = grade + 1 where course_id = 1 returning *;
-- case: pg-delete-using
delete from enrollment e using student s where s.id = e.student_id and s.name = 'Ada' returning e.*;
-- case: pg-update-from
update enrollment e set grade = least(e.grade + 5, 100) from course c where c.id = e.course_id and c.code = 'COMP1002';
-- case: pg-distinct-on
select distinct on (course_id) course_id, student_id, grade from enrollment order by course_id, grade desc;
-- case: pg-fetch-first
select * from student order by id offset 1 rows fetch first 2 rows only;
-- case: pg-full-outer-join
select * from student s full outer join enrollment e on e.student_id = s.id;
-- case: pg-nulls-last
select * from enrollment order by grade desc nulls last;
-- case: pg-filter
select course_id, count(*) filter (where grade >= 60) as passed, string_agg(student_id::text, ',' order by student_id) from enrollment group by course_id;
-- case: pg-grouping-sets
select course_id, student_id, count(*) from enrollment group by grouping sets ((course_id), (student_id), ()), rollup (course_id), cube (student_id);
-- case: pg-on-conflict
insert into student (id, name) values (1, 'Ann') on conflict (id) do update set name = excluded.name;
-- case: pg-on-conflict-nothing
insert into student values (1, 'Dup') on conflict do nothing;
-- case: pg-identity
create table t_id (id int generated always as identity primary key, s serial, b bigserial, t text not null default '', ts timestamptz default now(), j jsonb, a int[], u uuid, n numeric(8,2) check (n >= 0));
-- case: pg-create-table-fk
create table tutor (id serial primary key, name varchar(40) not null, course_id int references course(id) on delete cascade, unique (name), constraint ck check (id > 0));
-- case: pg-alter-type
alter table student alter column name type varchar(80), alter column name set not null, add column email text, drop column if exists nothing;
-- case: pg-rename
alter table student rename column name to full_name;
-- case: pg-truncate-restart
truncate table enrollment restart identity cascade;
-- case: pg-create-type
create type mood as enum ('sad', 'ok', 'happy');
-- case: pg-create-index-gin
create index concurrently if not exists idx_j on t_id using gin (j) where id > 0;
-- case: pg-create-view
create or replace view v as select s.name, e.grade from student s join enrollment e on e.student_id = s.id;
-- case: pg-materialized-view
create materialized view mv as select course_id, avg(grade) from enrollment group by course_id with no data;
-- case: pg-dollar-function
create function passed(g numeric) returns boolean language sql immutable as $$ select g >= 60 $$;
-- case: pg-plpgsql-function
create or replace function bump(cid int) returns int language plpgsql as $body$
declare n int;
begin
  update enrollment set grade = grade + 1 where course_id = cid;
  select count(*) into n from enrollment where course_id = cid;
  return n;
end;
$body$;
-- case: pg-trigger
create trigger cap before insert on enrollment for each row execute function cap_grade();
-- case: pg-begin-atomic
create function two() returns int language sql begin atomic select 1; select 2; end;
-- case: pg-do-block
do $$ begin raise notice 'hi'; end $$;
-- case: pg-procedure-call
call bump(1);
-- case: pg-explain-analyze
explain (analyze, buffers, format json) select * from student;
-- case: pg-array-json
select array[1,2,3] || 4, (array[1,2])[1], '{"a":1}'::jsonb -> 'a', '{"a":1}'::jsonb ->> 'a', j @> '{}' from t_id;
-- case: pg-values
values (1, 'a'), (2, 'b');
-- case: pg-table
table student;
-- case: pg-with-insert-returning
with moved as (delete from enrollment where grade < 60 returning *) insert into enrollment select * from moved;
-- case: pg-copy
copy student (id, name) from stdin with (format csv);
-- case: pg-set
set search_path to public; set local statement_timeout = '5s'; show search_path; reset all;
-- case: pg-isolation
begin isolation level serializable read write; commit and chain;
-- case: pg-grant
grant select, insert on student to public; revoke all on student from public; create role r login password 'x';
-- case: pg-schema
create schema if not exists s2 authorization postgres; drop schema s2 cascade;
-- case: pg-comment-on
comment on table student is 'students'; comment on column student.name is 'full name';
-- case: pg-escape-string
select E'a\nb', 'it''s', U&'\0041', $$dollar 'quoted'$$, $tag$a;b$tag$, b'101', x'1F';
-- case: pg-nested-comment
select 1 /* outer /* inner */ still comment */ + 2;
-- case: pg-quoted-identifier
select "name" as "Student Name" from "student";
-- case: pg-interval-extract
select extract(year from now()), date_trunc('month', now()), now() - interval '2 months', age(timestamp '2000-01-01'), to_char(now(), 'YYYY-MM-DD');
-- case: pg-coalesce-nullif
select coalesce(null, name), nullif(id, 1), greatest(1, 2), length(name), substring(name from 1 for 2), position('a' in name), name || '-' || id from student;
-- case: pg-any-array
select * from student where id = any (array[1, 2]) and name <> all ('{x,y}'::text[]);
-- case: pg-window-filter
select sum(grade) filter (where grade > 60) over (partition by course_id order by grade groups between 1 preceding and current row exclude ties) from enrollment;
-- case: pg-merge
merge into student s using (values (1, 'M')) as v(id, name) on s.id = v.id when matched then update set name = v.name when not matched then insert values (v.id, v.name);
-- case: pg-lateral-join
select s.name, t.g from student s left join lateral (select max(grade) g from enrollment e where e.student_id = s.id) t on true;
-- case: pg-tablesample
select * from student tablesample bernoulli (50) repeatable (1);
