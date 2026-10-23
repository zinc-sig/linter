import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.stream.Collectors;

// Warm-up sample for the resident javac worker: generics, lambdas, streams
// and a deliberate type error load most of the attribution and diagnostic
// code before the first student lint.
public class Warmup {
    interface Shape {
        double area();
    }

    static Map<String, Integer> count(List<String> words) {
        Map<String, Integer> counts = new TreeMap<>();
        for (String w : words) {
            counts.merge(w.strip().toLowerCase(), 1, Integer::sum);
        }
        return counts;
    }

    public static void main(String[] args) {
        List<Shape> shapes = new ArrayList<>();
        shapes.add(() -> 2.0);
        double total = shapes.stream().mapToDouble(Shape::area).sum();
        String joined = count(List.of(args)).keySet().stream().collect(Collectors.joining(","));
        int wrong = joined;
        System.out.println(total + joined + wrong);
    }
}
