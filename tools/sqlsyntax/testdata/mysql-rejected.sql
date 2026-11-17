-- Statements MySQL 8.4 rejects with a syntax error. Each case sits under a
-- "-- case:" line.
-- case: json
select json_object('a', 1)->'$.a', json_extract('{"a":1}', '$.a'), json_arrayagg(id) from student;
-- case: typo-select
selec id from student;
-- case: typo-from
select id form student;
-- case: missing-from-table
select * from where id = 1;
-- case: trailing-comma
select id, name, from student;
-- case: missing-comma
select id name grade from student;
-- case: unbalanced-paren
select count(* from student;
-- case: extra-paren
select (id)) from student;
-- case: group-before-where
select course_id, count(*) from enrollment group by course_id where grade > 1;
-- case: having-before-group
select course_id from enrollment having count(*) > 1 group by course_id;
-- case: order-twice
select * from student order by id order by name;
-- case: join-no-table
select * from student join on student.id = 1;
-- case: unclosed-string
select 'abc from student;
-- case: pg-cast
select grade::int from enrollment;
-- case: pg-returning
insert into student values (20, 'Zed') returning id;
-- case: fetch-first
select * from student fetch first 2 rows only;
-- case: full-outer-join
select * from student s full outer join enrollment e on e.student_id = s.id;
-- case: top
select top 2 * from student;
-- case: insert-no-values
insert into student (id, name) (1, 'x');
-- case: update-no-set
update student name = 'x' where id = 1;
-- case: delete-star
delete * from student;
-- case: create-table-trailing-comma
create table bad1 (a int, b int,);
-- case: create-table-no-type
create table bad2 (a, b int);
-- case: alter-junk
alter table student add;
-- case: case-no-end
select case when id = 1 then 'a' from student;
-- case: in-empty
select * from student where id in ();
-- case: double-where
select * from student where id = 1 where name = 'a';
-- case: keyword-as-column
select from from student;
-- case: limit-expr
select * from student limit;
-- case: between
select * from enrollment where grade between 1;
-- case: window-syntax
select rank() over partition by course_id from enrollment;
-- case: cte-missing-as
with t (select 1) select * from t;
-- case: union-dangling
select 1 union;
-- case: ilike
select * from student where name ilike 'a%';
-- case: distinct-on
select distinct on (course_id) * from enrollment;
-- case: serial-pk-identity
create table bad3 (id int generated always as identity primary key);
-- case: nulls-last
select * from enrollment order by grade desc nulls last;
-- case: ansi-quotes-identifier
select "name" from "student";
