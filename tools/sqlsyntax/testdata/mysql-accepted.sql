-- Statements MySQL 8.4 parses without a syntax error. Each case is one
-- statement, or a few, under a "-- case:" line.
-- case: select-basic
select id, name from student where id > 2 order by name desc limit 3;
-- case: select-join-group-having
select c.code, count(*) as n, round(avg(e.grade), 1) as avg_grade from enrollment e join course c on c.id = e.course_id group by c.code having count(*) >= 3;
-- case: left-join-using
select s.name, e.grade from student s left join enrollment e using (id);
-- case: natural-join
select * from student natural join enrollment;
-- case: cross-straight-join
select * from student straight_join course cross join enrollment;
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
-- case: functions
select concat(name, '-', id), ifnull(null, 0), coalesce(null, name), upper(name), length(name), substring(name, 1, 2), if(id > 2, 'a', 'b') from student;
-- case: date-functions
select date_add(curdate(), interval 1 day), date_format(now(), '%Y-%m-%d'), datediff('2026-01-01', '2025-01-01'), year(now()), extract(month from now());
-- case: cast-convert
select cast(grade as decimal(5,2)), convert(grade, signed), cast('2026-01-01' as date), convert('abc' using utf8mb4) from enrollment;
-- case: operators
select * from enrollment where grade between 60 and 80 and student_id <=> 1 or grade div 10 = 7 or grade mod 2 = 0 or not (grade <> 5) or grade != 3;
-- case: like-regexp
select * from student where name like 'A%' escape '\\' or name regexp '^[A-C]' or name not like '%z' or name rlike 'a$';
-- case: is-null
select * from student where name is not null and id is null;
-- case: group-concat
select course_id, group_concat(distinct student_id order by student_id desc separator ', ') from enrollment group by course_id;
-- case: rollup
select course_id, count(*) from enrollment group by course_id with rollup;
-- case: distinct-count
select count(distinct course_id), sum(grade), min(grade), max(grade), std(grade) from enrollment;
-- case: limit-offset
select * from student order by id limit 2 offset 1;
-- case: limit-comma
select * from student order by id limit 1, 2;
-- case: backticks
select `s`.`name` as `student name` from `student` as `s`;
-- case: double-quoted-string
select "hello" as greeting, 'it''s' as q, 'a\'b' as r;
-- case: comments
select id /* inline */ from student # trailing hash
-- trailing dashes
where id = 1;
-- case: for-update
select * from student where id = 1 for update;
-- case: lateral
select s.name, t.g from student s, lateral (select max(grade) g from enrollment e where e.student_id = s.id) t;
-- case: values-row
values row(1, 'a'), row(2, 'b');
-- case: table-stmt
table student order by id limit 2;
-- case: user-variable
set @n := 3;
-- case: select-into-var
select count(*) into @c from student;
-- case: insert-values
insert into student (id, name) values (7, 'Gil'), (8, 'Hal');
-- case: insert-select
insert into student (id, name) select id + 100, name from student where id < 3;
-- case: insert-set
insert into student set id = 9, name = 'Ivy';
-- case: insert-on-duplicate
insert into student (id, name) values (1, 'Ann') on duplicate key update name = values(name);
-- case: insert-ignore
insert ignore into student values (1, 'Dup');
-- case: replace
replace into student (id, name) values (1, 'Rep');
-- case: update-simple
update enrollment set grade = grade + 5 where course_id = 2;
-- case: update-join
update enrollment e join course c on c.id = e.course_id set e.grade = least(e.grade + 5, 100) where c.code = 'COMP1002';
-- case: update-order-limit
update enrollment set grade = 0 order by grade limit 1;
-- case: delete-simple
delete from enrollment where grade < 60;
-- case: delete-join
delete e from enrollment e join student s on s.id = e.student_id where s.name = 'Ada';
-- case: delete-limit
delete from enrollment order by grade limit 2;
-- case: create-table
create table tutor (id int not null auto_increment, name varchar(40) not null default 'x', course_id int, hired date, pay decimal(8,2) check (pay >= 0), kind enum('ta','ra') not null, primary key (id), unique key uk (name), constraint fk_t foreign key (course_id) references course(id) on delete cascade on update restrict) engine=InnoDB default charset=utf8mb4;
-- case: create-table-if-not-exists
create table if not exists t2 (a int primary key, b text, c timestamp default current_timestamp on update current_timestamp, d int generated always as (a * 2) stored);
-- case: create-table-as
create table t3 as select * from student;
-- case: create-table-like
create table t4 like student;
-- case: create-temporary
create temporary table tmp (a int);
-- case: alter-add-column
alter table student add column email varchar(80) null after name, add index idx_name (name);
-- case: alter-modify
alter table student modify name varchar(60) not null, change column id sid int;
-- case: alter-drop
alter table enrollment drop foreign key enrollment_ibfk_1, drop column grade;
-- case: alter-add-constraint
alter table enrollment add constraint chk_grade check (grade between 0 and 100);
-- case: alter-rename
alter table student rename to pupil;
-- case: rename-table
rename table student to learner;
-- case: drop-table
drop table if exists enrollment, nothing_here;
-- case: truncate
truncate table enrollment;
-- case: create-index
create unique index idx_code on course (code);
-- case: create-fulltext-index
create fulltext index ft_name on student (name);
-- case: drop-index
drop index idx_code on course;
-- case: create-view
create or replace view v as select s.name, e.grade from student s join enrollment e on e.student_id = s.id;
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
-- case: set-autocommit
set autocommit = 0;
-- case: set-isolation
set session transaction isolation level repeatable read;
-- case: lock-tables
lock tables student read, course write;
-- case: unlock-tables
unlock tables;
-- case: explain
explain select * from student where id = 1;
-- case: show-tables
show tables;
-- case: show-create
show create table student;
-- case: describe
describe student;
-- case: use
use mysql;
-- case: prepare
prepare st from 'select * from student where id = ?';
-- case: grant
grant select on student to 'u'@'localhost';
-- case: create-user
create user 'u2'@'localhost' identified by 'pw';
-- case: create-database
create database if not exists d2;
-- case: trigger-single
create trigger cap before insert on enrollment for each row set new.grade = least(new.grade, 100);
-- case: function-single
create function passed(g decimal(4,1)) returns int deterministic return g >= 60;
-- case: procedure-single
create procedure top_n(in n int) select name from student order by id limit n;
-- case: call
call top_n(2);
-- case: procedure-compound
create procedure bump(in cid int)
begin
  declare n int default 0;
  update enrollment set grade = grade + 1 where course_id = cid;
  select count(*) into n from enrollment where course_id = cid;
  if n > 3 then select 'many'; else select 'few'; end if;
end;
-- case: trigger-compound
create trigger audit after update on enrollment for each row
begin
  if new.grade < 0 then signal sqlstate '45000' set message_text = 'negative'; end if;
end;
-- case: drop-procedure
drop procedure if exists bump;
-- case: any-all
select * from enrollment where grade > all (select grade from enrollment where course_id = 2) or grade = any (select 1) or grade <> some (select 2);
-- case: row-compare
select * from enrollment where (student_id, course_id) in ((1, 1), (2, 3)) and row(1, 2) = row(1, 2);
-- case: not-in-exists
select * from student s where id not in (1, 2) and not exists (select * from enrollment e where e.student_id = s.id);
-- case: insert-values-row
insert into student values row(30, 'Ro'), row(31, 'Wy');
-- case: insert-alias-dup
insert into student (id, name) values (1, 'N') as new on duplicate key update name = new.name;
-- case: cte-update
with low as (select student_id from enrollment where grade < 60) update enrollment set grade = 60 where student_id in (select student_id from low);
-- case: cte-delete
with low as (select student_id from enrollment where grade < 60) delete from enrollment where student_id in (select student_id from low);
-- case: delete-using
delete from e using enrollment e join student s on s.id = e.student_id where s.id = 1;
-- case: partition
create table p1 (id int, y int) partition by range (y) (partition p0 values less than (2000), partition p1 values less than maxvalue);
-- case: generated-virtual
create table g1 (a int, b int generated always as (a + 1) virtual, c int as (a * 2) stored not null);
-- case: check-not-enforced
create table c1 (a int, constraint ck check (a > 0) not enforced);
-- case: alter-algorithm
alter table student add column z int, algorithm = inplace, lock = none;
-- case: alter-order
alter table student add column first_col int first, drop primary key, add primary key (id, first_col);
-- case: alter-default
alter table student alter column name set default 'n', alter column name drop default;
-- case: alter-rename-column
alter table student rename column name to full_name, rename index `PRIMARY` to p;
-- case: analyze-table
analyze table student;
-- case: show-index
show index from student;
-- case: show-columns
show full columns from student like 'n%';
-- case: show-variables
show variables like 'sql_mode';
-- case: explain-format
explain format = json select * from student;
-- case: explain-analyze
explain analyze select * from student;
-- case: for-share
select * from student for share skip locked;
-- case: nowait
select * from student for update nowait;
-- case: grouping
select course_id, grouping(course_id), count(*) from enrollment group by course_id with rollup;
-- case: json-table
select jt.* from json_table('[{"a":1}]', '$[*]' columns (a int path '$.a')) as jt;
-- case: member-of
select 1 member of ('[1,2]');
-- case: regexp-like
select regexp_like(name, '^a'), regexp_replace(name, 'a', 'b') from student;
-- case: cast-unsigned
select cast(grade as unsigned), cast(grade as char(10)), cast(grade as signed integer), cast(1 as json), cast('1' as float), cast('1' as double) from enrollment;
-- case: assign-in-select
select @r := @r + 1 as rn, name from student, (select @r := 0) init;
-- case: interval-arith
select now() + interval 1 day, '2026-01-01' - interval 2 month, timestampdiff(year, '2000-01-01', curdate()), str_to_date('01,5,2013', '%d,%m,%Y');
-- case: literals
select 0xFF, b'1010', x'4D', _utf8mb4'x', n'x', 1e3, .5, true, false, null, current_timestamp, current_date;
-- case: collate-binary
select name collate utf8mb4_bin, binary name from student order by name collate utf8mb4_general_ci;
-- case: index-hint
select * from student use index (primary) ignore index for order by (primary) where id = 1;
-- case: optimizer-hint
select /*+ no_index(student primary) */ * from student;
-- case: select-options
select distinct sql_calc_found_rows high_priority id from student;
-- case: view-check-option
create view v2 (a, b) as select id, name from student where id > 1 with cascaded check option;
-- case: view-algorithm
create algorithm = merge definer = current_user sql security invoker view v3 as select 1;
-- case: set-names
set names utf8mb4 collate utf8mb4_unicode_ci;
-- case: set-session
set session sql_mode = 'ANSI_QUOTES', @@session.max_join_size = 10;
-- case: set-subquery
set @m = (select max(grade) from enrollment);
-- case: execute-using
execute st using @a, @b;
-- case: deallocate
deallocate prepare st;
-- case: flush
flush privileges;
-- case: revoke
revoke all privileges on t.* from 'u'@'localhost';
-- case: create-role
create role 'r1';
-- case: drop-user
drop user if exists 'u'@'localhost';
-- case: alter-user
alter user 'u'@'localhost' identified by 'new';
-- case: do
do sleep(0);
-- case: optimize
optimize table student;
-- case: check-table
check table student;
-- case: select-outfile
select * from student into outfile '/tmp/x';
-- case: load-data
load data infile '/tmp/x' into table student fields terminated by ',';
-- case: signal
signal sqlstate '45000' set message_text = 'x';
-- case: get-diagnostics
get diagnostics @n = number;
-- case: create-event
create event ev on schedule every 1 day do delete from enrollment where grade < 0;
-- case: handler
handler student open;
-- case: xa
xa start 'x1';
-- case: drop-trigger
drop trigger if exists cap;
-- case: show-procedure
show procedure status;
-- case: nested-parens-union
(select id from student order by id limit 1) union (select id from course) order by 1 limit 3;
-- case: join-nested
select * from student s join (enrollment e join course c on c.id = e.course_id) on e.student_id = s.id;
-- case: odbc-join
select * from { oj student s left outer join enrollment e on e.student_id = s.id };
-- case: if-exists-func
select exists (select 1 from student), not exists (select 1 from course), isnull(null), nullif(1, 1), greatest(1, 2), char_length('a'), lpad('a', 3, 'x'), trim(both 'x' from 'xax'), position('a' in 'ba');
-- case: insert-default
insert into student values (default, default), (40, default);
-- case: update-multi
update student s, enrollment e set s.name = 'x', e.grade = 1 where s.id = e.student_id;
-- case: create-schema
create schema s2 default character set utf8mb4;
-- case: select-no-from-where
select 1 where 1 = 1;
-- case: frame-range
select sum(grade) over (order by grade range between unbounded preceding and current row), ntile(4) over (order by grade), first_value(grade) over w, nth_value(grade, 2) over w from enrollment window w as (order by grade);
